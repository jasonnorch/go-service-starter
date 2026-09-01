package health_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jasonnorch/go-service-starter/internal/health"
)

func discard() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

type body struct {
	Status string            `json:"status"`
	Checks map[string]string `json:"checks"`
}

func probe(t *testing.T, h http.Handler) (int, body) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", http.NoBody))

	var b body
	if err := json.Unmarshal(rec.Body.Bytes(), &b); err != nil {
		t.Fatalf("probe response is not JSON (%v): %s", err, rec.Body.String())
	}
	return rec.Code, b
}

// A liveness probe that consults a dependency restarts every instance at once
// when that dependency blips, which is why it consults nothing.
func TestLiveIgnoresRegisteredChecks(t *testing.T) {
	checker := health.New(discard(), time.Second)
	checker.Register("database", func(context.Context) error {
		t.Error("a readiness check ran on the liveness probe")
		return errors.New("down")
	})

	status, got := probe(t, checker.Live())

	if status != http.StatusOK {
		t.Errorf("status = %d, want 200 — a live process reported itself dead", status)
	}
	if got.Status != "ok" {
		t.Errorf("status = %q, want ok", got.Status)
	}
}

func TestReadyWithNoChecks(t *testing.T) {
	status, got := probe(t, health.New(discard(), time.Second).Ready())

	if status != http.StatusOK || got.Status != "ok" {
		t.Errorf("status = %d %q, want 200 ok", status, got.Status)
	}
}

func TestReadyPassesWhenEveryCheckPasses(t *testing.T) {
	checker := health.New(discard(), time.Second)
	checker.Register("database", func(context.Context) error { return nil })
	checker.Register("cache", func(context.Context) error { return nil })

	status, got := probe(t, checker.Ready())

	if status != http.StatusOK {
		t.Errorf("status = %d, want 200", status)
	}
	if got.Checks["database"] != "ok" || got.Checks["cache"] != "ok" {
		t.Errorf("checks = %v", got.Checks)
	}
}

// 503 rather than 500: the instance is healthy and should be left running,
// just taken out of rotation.
func TestReadyFailsWith503WhenACheckFails(t *testing.T) {
	checker := health.New(discard(), time.Second)
	checker.Register("database", func(context.Context) error { return nil })
	checker.Register("cache", func(context.Context) error { return errors.New("connection refused") })

	status, got := probe(t, checker.Ready())

	if status != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", status)
	}
	if got.Status != "failed" {
		t.Errorf("overall status = %q, want failed", got.Status)
	}
	if got.Checks["cache"] != "failed" {
		t.Errorf("cache = %q, want failed", got.Checks["cache"])
	}
	// A passing check still reports as passing, so the failure can be located.
	if got.Checks["database"] != "ok" {
		t.Errorf("database = %q, want ok", got.Checks["database"])
	}
}

// The response says which check failed but never why: a readiness endpoint is
// routinely reachable from further away than its authors assume.
func TestReadyKeepsFailureDetailOutOfTheResponse(t *testing.T) {
	var logged bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logged, &slog.HandlerOptions{Level: slog.LevelDebug}))

	checker := health.New(logger, time.Second)
	checker.Register("database", func(context.Context) error {
		return errors.New("dial tcp 10.0.1.7:5432: connection refused")
	})

	rec := httptest.NewRecorder()
	checker.Ready().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", http.NoBody))

	if strings.Contains(rec.Body.String(), "10.0.1.7") {
		t.Errorf("infrastructure detail reached the response: %s", rec.Body.String())
	}
	if !strings.Contains(logged.String(), "10.0.1.7") {
		t.Errorf("the reason was not logged, so a failing probe cannot be diagnosed: %s", logged.String())
	}
}

// Sequentially, the worst case is the sum of every dependency's timeout — which
// is how a readiness endpoint ends up slower than the interval it is polled at.
func TestReadyRunsChecksConcurrently(t *testing.T) {
	const each = 150 * time.Millisecond

	checker := health.New(discard(), time.Second)
	for _, name := range []string{"a", "b", "c", "d"} {
		checker.Register(name, func(ctx context.Context) error {
			select {
			case <-time.After(each):
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
	}

	start := time.Now()
	status, _ := probe(t, checker.Ready())
	elapsed := time.Since(start)

	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if elapsed > 3*each {
		t.Errorf("four %s checks took %s; they ran sequentially", each, elapsed)
	}
}

// A check that ignores its deadline would otherwise hold the probe open, which
// is indistinguishable from the service being down.
func TestReadyBoundsSlowChecksByTheTimeout(t *testing.T) {
	checker := health.New(discard(), 50*time.Millisecond)
	checker.Register("slow", func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	})

	start := time.Now()
	status, got := probe(t, checker.Ready())

	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("the probe took %s; the timeout was not applied", elapsed)
	}
	if status != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503 for a timed-out check", status)
	}
	if got.Checks["slow"] != "failed" {
		t.Errorf("slow = %q, want failed", got.Checks["slow"])
	}
}

func TestRegisterReplacesAnExistingCheck(t *testing.T) {
	checker := health.New(discard(), time.Second)
	checker.Register("database", func(context.Context) error { return errors.New("down") })
	checker.Register("database", func(context.Context) error { return nil })

	status, got := probe(t, checker.Ready())

	if status != http.StatusOK {
		t.Errorf("status = %d, want 200 — the replaced check still ran", status)
	}
	if len(got.Checks) != 1 {
		t.Errorf("checks = %v, want one entry", got.Checks)
	}
}

// A nil check would be called as a nil function during a probe — and that panic
// happens in a goroutine of its own, where no HTTP middleware can recover it.
// The process dies at probe time, remote from the wiring mistake that caused it.
func TestRegisterHandlesANilCheck(t *testing.T) {
	var logged bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logged, &slog.HandlerOptions{Level: slog.LevelDebug}))

	checker := health.New(logger, time.Second)
	checker.Register("database", nil)

	// Reported at registration, not at the first probe. The distinction is the
	// point: the mistake is in the wiring, and saying so at startup is what
	// stops it being diagnosed later from a probe failure.
	//
	// Asserted on the specific message rather than on the word "nil", because
	// run's panic recovery also mentions nil — the two are independent
	// protections and a test that cannot tell them apart passes when either is
	// removed.
	if !strings.Contains(logged.String(), "registered as nil") {
		t.Errorf("registration did not report the nil check: %s", logged.String())
	}

	status, got := probe(t, checker.Ready())

	if status != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503 — a check that was never supplied cannot pass", status)
	}
	if got.Checks["database"] != "failed" {
		t.Errorf("database = %q, want failed", got.Checks["database"])
	}
}

// The timeout has to bound the probe, not merely be offered to the checks.
//
// The existing slow-check test uses a check that honors ctx, so it proves the
// cooperative path only. A driver with its own timeout, or a library that forgot
// one, holds the probe open for as long as it likes — and then reports success,
// so /readyz answers 200 long after it should have answered at all.
func TestReadyDoesNotWaitForACheckThatIgnoresTheContext(t *testing.T) {
	const timeout = 50 * time.Millisecond
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })

	checker := health.New(discard(), timeout)
	checker.Register("cooperative", func(context.Context) error { return nil })
	checker.Register("stubborn", func(context.Context) error {
		<-release // never returns during the test, and never looks at ctx
		return nil
	})

	start := time.Now()
	status, got := probe(t, checker.Ready())
	elapsed := time.Since(start)

	if elapsed > time.Second {
		t.Errorf("the probe took %s against a %s timeout; it waited for the stubborn check", elapsed, timeout)
	}
	if status != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", status)
	}
	if got.Checks["stubborn"] != "failed" {
		t.Errorf("stubborn = %q, want failed", got.Checks["stubborn"])
	}
	// The one that answered in time is still reported on its own merits.
	if got.Checks["cooperative"] != "ok" {
		t.Errorf("cooperative = %q, want ok", got.Checks["cooperative"])
	}
}

// A check runs in a goroutine outside the request's, so a panic in one is
// unrecoverable by any HTTP middleware and ends the process. A dependency that
// panics on a bad connection should turn readiness red, not stop the service.
func TestReadySurvivesAPanickingCheck(t *testing.T) {
	var logged bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logged, &slog.HandlerOptions{Level: slog.LevelDebug}))

	checker := health.New(logger, time.Second)
	checker.Register("healthy", func(context.Context) error { return nil })
	checker.Register("panicky", func(context.Context) error { panic("driver exploded") })

	status, got := probe(t, checker.Ready())

	if status != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", status)
	}
	if got.Checks["panicky"] != "failed" {
		t.Errorf("panicky = %q, want failed", got.Checks["panicky"])
	}
	if got.Checks["healthy"] != "ok" {
		t.Errorf("healthy = %q, want ok — one bad check must not condemn the others", got.Checks["healthy"])
	}
	if !strings.Contains(logged.String(), "driver exploded") {
		t.Errorf("the panic value was not logged: %s", logged.String())
	}
	// And it is not published, like every other failure reason.
	if strings.Contains(fmt.Sprint(got.Checks), "driver exploded") {
		t.Error("the panic value reached the response")
	}
}
