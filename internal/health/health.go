// Package health serves the liveness and readiness probes.
//
// The two answer different questions, and conflating them is the single most
// common way a health check makes an outage worse:
//
//   - Liveness asks "is this process working?" A failure means restart me. It
//     must never consult a dependency: a database blip that fails a liveness
//     probe restarts every instance of the service at once, turning a recovered
//     dependency into a cold start of the entire fleet.
//   - Readiness asks "should traffic come here now?" A failure means take me
//     out of rotation and leave me running. This is where dependencies belong.
package health

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/jasonnorch/go-service-starter/internal/httpapi/respond"
)

// CheckFunc reports whether a dependency is usable. It must honor ctx: a probe
// that blocks past its deadline is indistinguishable from the service being
// down, and it holds a connection open while saying so.
type CheckFunc func(ctx context.Context) error

// Checker holds the registered readiness checks.
//
// Checks are registered rather than compiled in, so each dependency added in a
// follow-up — Postgres, Redis, an upstream API — contributes its own check from
// its own package and this one never grows an import of it.
type Checker struct {
	logger  *slog.Logger
	timeout time.Duration

	mu     sync.RWMutex
	checks map[string]CheckFunc
}

// New returns a Checker whose readiness probes are bounded by timeout.
func New(logger *slog.Logger, timeout time.Duration) *Checker {
	return &Checker{
		logger:  logger,
		timeout: timeout,
		checks:  make(map[string]CheckFunc),
	}
}

// Register adds a readiness check. A repeated name replaces the earlier check.
//
// A nil check is recorded as one that always fails, and logged at registration.
// Storing it as-is would call a nil function during a probe, and that panic
// happens in a goroutine of its own where no HTTP middleware can recover it —
// the process dies, at probe time, rather than at the wiring mistake that
// caused it.
//
// The standard library panics on the equivalent mistake (http.ServeMux.Handle
// on a nil handler), and doing so here would surface it faster. This repo's
// conventions say not to panic in library code, and the alternative is
// defensible on its own terms: the service starts, readiness goes red, no
// traffic is routed to it, and the log says exactly which check was never
// supplied. A misconfigured instance that refuses traffic beats one that
// crash-loops.
func (c *Checker) Register(name string, check CheckFunc) {
	if check == nil {
		c.logger.Error("readiness check registered as nil; it will always fail", "check", name)
		check = func(context.Context) error {
			return errors.New("no check function was registered")
		}
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.checks[name] = check
}

// statusResponse is what both probes return.
//
// Checks report only whether they passed. The reason goes to the log instead,
// because a readiness endpoint is routinely reachable from further away than
// its authors assume, and one that prints "dial tcp 10.0.1.7:5432: refused" is
// a free map of the infrastructure behind it. Whoever is debugging a failing
// probe has the logs; whoever is scanning for a way in does not.
type statusResponse struct {
	Status string            `json:"status"`
	Checks map[string]string `json:"checks,omitempty"`
}

const (
	statusOK     = "ok"
	statusFailed = "failed"
)

// Live handles the liveness probe. It reports success as long as the process is
// running and able to serve — which is the entire question it is asked.
func (c *Checker) Live() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		respond.JSON(w, c.logger, http.StatusOK, statusResponse{Status: statusOK})
	})
}

// Ready handles the readiness probe, running every registered check.
//
// Checks run concurrently under one shared deadline. Sequentially, a probe's
// worst case is the sum of its dependencies' timeouts, which is how a readiness
// endpoint ends up slower than the interval it is polled at.
func (c *Checker) Ready() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), c.timeout)
		defer cancel()

		c.mu.RLock()
		checks := maps.Clone(c.checks)
		c.mu.RUnlock()

		results := c.run(ctx, checks)

		status := http.StatusOK
		body := statusResponse{Status: statusOK, Checks: make(map[string]string, len(results))}
		for _, name := range slices.Sorted(maps.Keys(results)) {
			if results[name] == nil {
				body.Checks[name] = statusOK
				continue
			}
			body.Checks[name] = statusFailed
			body.Status = statusFailed
			status = http.StatusServiceUnavailable
		}

		respond.JSON(w, c.logger, status, body)
	})
}

// outcome is one check's result, carried back from its goroutine.
type outcome struct {
	name string
	err  error
}

// run executes every check concurrently and returns each one's result, bounded
// by ctx.
//
// It deliberately does not wait for every goroutine to finish. Waiting makes
// the deadline advisory: a check that ignores its context — a driver with its
// own timeout, a library that forgot one — holds the probe open for as long as
// it likes and then reports success, so /readyz answers 200 long after it
// should have answered at all. An orchestrator polling every two seconds does
// not care what the answer would eventually have been.
//
// So anything still running when ctx expires is reported as failed and left to
// finish on its own. Its goroutine is not leaked in the blocking sense: the
// results channel is buffered for every check, so a late send never blocks and
// the goroutine exits whenever its check returns.
func (c *Checker) run(ctx context.Context, checks map[string]CheckFunc) map[string]error {
	results := make(chan outcome, len(checks))

	for name, check := range checks {
		go func() {
			// A panicking check would otherwise take the process down: this
			// goroutine is outside the request's, so no HTTP middleware can
			// recover it. A dependency that panics on a bad connection should
			// make readiness go red, not end the service.
			defer func() {
				if recovered := recover(); recovered != nil {
					results <- outcome{name, fmt.Errorf("check panicked: %v", recovered)}
				}
			}()

			results <- outcome{name, check(ctx)}
		}()
	}

	collected := make(map[string]error, len(checks))
	for range checks {
		select {
		case got := <-results:
			if got.err != nil {
				// The one place the reason is recorded, since the response
				// deliberately omits it.
				c.logger.WarnContext(ctx, "readiness check failed", "check", got.name, "error", got.err)
			}
			collected[got.name] = got.err

		case <-ctx.Done():
			for name := range checks {
				if _, done := collected[name]; !done {
					err := fmt.Errorf("check did not finish within the readiness timeout: %w", ctx.Err())
					c.logger.WarnContext(ctx, "readiness check timed out", "check", name, "error", err)
					collected[name] = err
				}
			}
			return collected
		}
	}

	return collected
}
