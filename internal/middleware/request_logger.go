// Package middleware holds the standard HTTP middleware the service installs on
// every request.
//
// Every constructor here returns a plain func(http.Handler) http.Handler rather
// than a named type from this repo. That is the portability claim made
// concrete: nothing in this package imports the router, so all of it composes
// with any stdlib-compatible middleware and survives the router being replaced.
// It also means the tests need no framework — an httptest.NewRecorder and an
// http.HandlerFunc are the whole harness.
package middleware

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/jasonnorch/go-service-starter/internal/requestid"
)

// RequestLogger writes one line per request, after it completes.
//
// One line, at the end, carrying the outcome: logging on the way in as well
// doubles the volume to say nothing, since a request that never completes is
// far better found by the absence of its line than by the presence of a second
// one. The fields are fixed so the log is queryable — a message that
// interpolates the path into free text cannot be aggregated by route.
//
// Install it outside Recover, so a panic that becomes a 500 is logged as a 500
// rather than as nothing at all.
func RequestLogger(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := newRecorder(w)

			next.ServeHTTP(rec, r)

			// A panic unwinding through here would skip this line entirely,
			// which is exactly why Recover belongs inside: it converts the
			// panic to a response before this returns.
			logger.LogAttrs(r.Context(), levelFor(rec.Status()), "request",
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", rec.Status()),
				slog.Int64("bytes", rec.BytesWritten()),
				slog.Duration("duration", time.Since(start)),
				slog.String("request_id", requestid.FromContext(r.Context())),
				// RemoteAddr is the peer's address, which behind a proxy is the
				// proxy. Trusting a forwarded header instead requires knowing
				// the proxy is the only way in, since anyone can send one — so
				// that decision is left to whoever deploys this.
				slog.String("remote_addr", r.RemoteAddr),
			)
		})
	}
}

// levelFor keeps server errors visible at a level that pages someone, without
// making a wall of 404s do the same.
func levelFor(status int) slog.Level {
	switch {
	case status >= http.StatusInternalServerError:
		return slog.LevelError
	case status >= http.StatusBadRequest:
		return slog.LevelWarn
	default:
		return slog.LevelInfo
	}
}
