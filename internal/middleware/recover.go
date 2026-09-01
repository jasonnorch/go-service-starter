package middleware

import (
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"

	"github.com/jasonnorch/go-service-starter/internal/httpapi/respond"
)

// Recover turns a panicking handler into a 500.
//
// This middleware is not optional, and it is the single largest thing a
// framework was doing for free. net/http's own recovery closes the connection
// and logs to the server's error log — the client gets no response at all, just
// a connection that ends mid-request, which surfaces as a network error and
// sends well-behaved clients into a retry loop against a bug that will panic
// again.
//
// Install it inside the request logger rather than outside, so the 500 it
// produces appears in the access log like any other response.
func Recover(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				recovered := recover()
				if recovered == nil {
					return
				}

				// ErrAbortHandler is net/http's documented way for a handler to
				// abandon a response on purpose; the server expects to see it
				// and suppresses it itself. Swallowing it here would turn a
				// deliberate abort into a 500.
				if recovered == http.ErrAbortHandler { //nolint:errorlint,err113 // compared by identity, exactly as net/http documents
					panic(recovered)
				}

				err := fmt.Errorf("panic: %v", recovered)
				logger.ErrorContext(r.Context(), "handler panicked",
					"error", err,
					"method", r.Method,
					"path", r.URL.Path,
					// The stack is the only reason this log line is worth
					// anything: the panic value alone rarely says where.
					"stack", string(debug.Stack()),
				)

				// Once the response has begun the status is already sent and
				// the body is partly written; appending an error envelope to it
				// produces something the client cannot parse. Logging is all
				// that is left.
				if observer, ok := w.(responseObserver); ok && observer.Written() {
					return
				}

				respond.Fail(w, r, logger, respond.Internal(err))
			}()

			next.ServeHTTP(w, r)
		})
	}
}
