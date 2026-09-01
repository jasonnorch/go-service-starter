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
func (c *Checker) Register(name string, check CheckFunc) {
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

// run executes every check concurrently and returns each one's result.
func (c *Checker) run(ctx context.Context, checks map[string]CheckFunc) map[string]error {
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		results = make(map[string]error, len(checks))
	)

	for name, check := range checks {
		wg.Go(func() {
			err := check(ctx)
			if err != nil {
				// The one place the reason is recorded, since the response
				// deliberately omits it.
				c.logger.WarnContext(ctx, "readiness check failed", "check", name, "error", err)
			}

			mu.Lock()
			defer mu.Unlock()
			results[name] = err
		})
	}

	wg.Wait()
	return results
}
