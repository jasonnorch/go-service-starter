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
	"time"

	"github.com/jasonnorch/go-service-starter/internal/config"
	"github.com/jasonnorch/go-service-starter/internal/health"
	"github.com/jasonnorch/go-service-starter/internal/httpapi"
	"github.com/jasonnorch/go-service-starter/internal/httpapi/respond"
	"github.com/jasonnorch/go-service-starter/internal/logging"
	"github.com/jasonnorch/go-service-starter/internal/middleware"
	"github.com/jasonnorch/go-service-starter/internal/postgres" // module:postgres
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

	// Readiness checks are registered here as dependencies are added; each
	// dependency supplies its own check, and internal/health never learns what
	// any of them is.
	checker := health.New(logger, cfg.Server.ReadTimeout)

	// Each module is one marked block here; `./setup.sh --without <module>`
	// deletes the block along with the package. See docs/decisions.md.

	// module:postgres begin
	pool, err := postgres.Open(ctx, cfg.Postgres, logger)
	if err != nil {
		return fmt.Errorf("opening postgres: %w", err)
	}
	// A defer, not appCtx: appCancel runs before the server drains, and the
	// requests still in flight need the pool until Serve returns.
	defer pool.Close()
	checker.Register("postgres", pool.Ping)
	// module:postgres end

	server := httpapi.NewServer(newRouter(cfg, logger, version, checker), cfg.Server)

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
func newRouter(cfg *config.Config, logger *slog.Logger, version string, checker *health.Checker) http.Handler {
	mux := http.NewServeMux()
	routes := httpapi.NewGroup(mux, logger)

	// Probe paths follow Kubernetes' current names; see docs/decisions.md,
	// "/livez and /readyz".
	routes.Handle("GET /livez", checker.Live())
	routes.Handle("GET /readyz", checker.Ready())
	routes.HandleFunc("GET /info", infoHandler(cfg, logger, version))

	// The global stack wraps the mux rather than each route, so it also covers
	// the requests that match no route at all. Applying it per-route instead
	// leaves 404s with no request ID, no security headers, and no log line —
	// which is the shape of a path-scanning attempt, and exactly what should
	// not be invisible.
	//
	// Fallback sits innermost of the wrappers, because ServeMux answers an
	// unmatched route with a plain-text 404 or 405 of its own: the only two
	// responses that would not be the JSON envelope, and the two a client meets
	// first.
	//
	// The order below is the order they run in, and each position is a
	// decision:
	//
	//   RequestID       first, so everything after it can report the ID
	//   SecurityHeaders before anything can write a response
	//   RequestLogger   outside Recover, so a panic is logged as the 500 it
	//                   becomes rather than as nothing at all
	//   Recover         inside the logger, outside every handler
	//   BodyLimit       innermost, closest to whatever reads the body
	return httpapi.Chain(
		httpapi.Fallback(mux, logger),
		middleware.RequestID(),
		middleware.SecurityHeaders(),
		middleware.RequestLogger(logger, "/livez", "/readyz"),
		middleware.Recover(logger),
		middleware.BodyLimit(cfg.Server.BodyLimit, logger),
	)
}

// info is what /info reports. It is deliberately not the whole *config.Config:
// config.Config carries json tags so a protected admin endpoint can marshal it
// directly, but an unauthenticated endpoint publishing the service's full
// configuration is a default nobody should inherit by forking.
type info struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Environment string `json:"environment"`
	Uptime      string `json:"uptime"`
}

func infoHandler(cfg *config.Config, logger *slog.Logger, version string) httpapi.HandlerFunc {
	started := time.Now()

	return func(w http.ResponseWriter, _ *http.Request) error {
		respond.JSON(w, logger, http.StatusOK, info{
			Name:        cfg.Service.Name,
			Version:     version,
			Environment: cfg.Service.Environment,
			Uptime:      time.Since(started).Round(time.Second).String(),
		})
		return nil
	}
}
