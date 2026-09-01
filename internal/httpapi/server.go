// Package httpapi holds the HTTP server's lifecycle, the router, and the small
// set of helpers that replace what a web framework would otherwise provide.
package httpapi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/jasonnorch/go-service-starter/internal/config"
)

// NewServer builds an http.Server from the configured parameters.
//
// Every timeout is set. An http.Server with none is the default, and it is a
// server that a single slow client can hold a connection on indefinitely.
func NewServer(handler http.Handler, cfg config.Server) *http.Server {
	return &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Port),
		Handler:      handler,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
		IdleTimeout:  cfg.IdleTimeout,
	}
}

// Serve runs the server until a signal arrives or it fails, then shuts down.
//
// The ordering is the whole point of the function:
//
//  1. A signal arrives, or ListenAndServe returns an error.
//  2. appCancel tears down application resources — background workers, pools —
//     so nothing new is started while the server drains.
//  3. Shutdown stops accepting connections and waits for in-flight requests,
//     bounded by shutdownTimeout.
//  4. A second signal, or an expired timeout, forces the connections closed.
//
// Step 4 is what stops a shutdown from hanging. A request that ignores its
// context will otherwise hold the process open for the whole grace period, and
// an operator who has already pressed Ctrl-C once expects the second press to
// mean it. sigCh must be buffered with room for that second signal — an
// unbuffered channel drops it, because signal.Notify never blocks.
//
// Returns nil on a clean shutdown, whatever caused it.
func Serve(
	server *http.Server,
	sigCh <-chan os.Signal,
	appCancel context.CancelFunc,
	logger *slog.Logger,
	shutdownTimeout time.Duration,
) error {
	serverErr := make(chan error, 1)
	go func() {
		logger.Info("server started", "addr", server.Addr)
		// ErrServerClosed is what Shutdown and Close cause, so it is the
		// success path here rather than a failure.
		if err := server.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
	}()

	select {
	case err := <-serverErr:
		appCancel()
		return fmt.Errorf("server error: %w", err)
	case sig := <-sigCh:
		logger.Info("shutdown signal received", "signal", sig.String())
	}

	appCancel()

	// Watches for a second signal for as long as the graceful shutdown below is
	// still running, and exits with it either way so the goroutine cannot
	// outlive the function.
	shutdownDone := make(chan struct{})
	go func() {
		select {
		case sig := <-sigCh:
			logger.Warn("second signal received, forcing close", "signal", sig.String())
			if err := server.Close(); err != nil {
				logger.Error("server close failed", "error", err)
			}
		case <-shutdownDone:
		}
	}()
	defer close(shutdownDone)

	// Deliberately not derived from the application context: that context has
	// just been canceled by appCancel, and a shutdown given an already-expired
	// deadline drains nothing.
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		logger.Error("graceful shutdown failed, forcing close", "error", err)
		if err := server.Close(); err != nil {
			logger.Error("server close failed", "error", err)
		}
	}

	logger.Info("server stopped")
	return nil
}
