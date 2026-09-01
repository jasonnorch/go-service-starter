package middleware

import (
	"fmt"
	"log/slog"
	"net/http"

	"github.com/jasonnorch/go-service-starter/internal/httpapi/respond"
)

// BodyLimit rejects request bodies larger than maxBytes.
//
// Two mechanisms, because neither is sufficient alone. The Content-Length check
// rejects an oversized request before a byte of it is read, which is the whole
// point — but Content-Length is absent on a chunked request and is a claim
// rather than a fact on any other. MaxBytesReader enforces the limit against
// what actually arrives.
//
// This is the service-wide ceiling. An endpoint that should accept far less
// says so through the limit it passes to decode.JSON.
func BodyLimit(maxBytes int64, logger *slog.Logger) func(http.Handler) http.Handler {
	tooLarge := fmt.Sprintf("request body must not exceed %d bytes", maxBytes)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.ContentLength > maxBytes {
				respond.Fail(w, r, logger, respond.PayloadTooLarge(tooLarge))
				return
			}

			if r.Body != nil {
				// MaxBytesReader needs w so it can mark the connection for
				// closing when the limit is hit; without it a client that keeps
				// sending leaves the connection in an undefined state.
				r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
			}

			next.ServeHTTP(w, r)
		})
	}
}
