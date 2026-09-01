// Package app wires the service together: it loads configuration, constructs
// every dependency in order, and runs the server until it is asked to stop.
//
// This is the only package that holds a whole *config.Config. Everything else
// receives the one sub-struct it needs, which keeps a package's configuration
// surface visible in its constructor signature rather than hidden behind a
// global lookup somewhere in its body.
package app

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/jasonnorch/go-service-starter/internal/config"
	"github.com/jasonnorch/go-service-starter/internal/httpapi"
	"github.com/jasonnorch/go-service-starter/internal/logging"
)

// Run starts the service and blocks until it shuts down.
//
// It returns errors rather than reporting them: until the configuration has
// loaded there is no configured logger to report through, and the caller has
// one for exactly that case. Anything Run logs itself, it logs once.
func Run(ctx context.Context, version string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading configuration: %w", err)
	}

	logger := logging.New(cfg.Log, os.Stdout).With(
		"service", cfg.Service.Name,
		"version", version,
	)
	logger.Info("starting",
		"environment", cfg.Service.Environment,
		"port", cfg.Server.Port,
	)

	// appCtx is what every background worker should take, so that appCancel in
	// httpapi.Serve tears them down before the server begins draining.
	//
	// It is also what Serve watches for shutdown. Being derived from ctx, it
	// carries the caller's cancellation through: canceling the context passed to
	// Run stops the server. Without that, Run would accept a context and quietly
	// ignore it, which is worse than not taking one — the caller has every
	// reason to believe cancellation works.
	appCtx, appCancel := context.WithCancel(ctx)
	defer appCancel()

	server := httpapi.NewServer(newRouter(appCtx, cfg, logger), cfg.Server)

	// Buffered for two: the second signal is what forces a hung shutdown to
	// finish, and signal.Notify drops signals rather than blocking on a full
	// channel — so an unbuffered channel would silently discard it.
	sigCh := make(chan os.Signal, 2)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	return httpapi.Serve(appCtx, server, sigCh, appCancel, logger, cfg.Server.ShutdownTimeout)
}

// newRouter assembles the handler tree. It lives here rather than in httpapi
// because it is the wiring, not the machinery — this is the function a fork
// edits to add routes.
func newRouter(_ context.Context, _ *config.Config, _ *slog.Logger) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
	return mux
}
