package middleware

import (
	"crypto/rand"
	"net/http"
	"regexp"
	"strings"

	"github.com/jasonnorch/go-service-starter/internal/requestid"
)

// maxRequestIDLen caps an accepted client-supplied ID after trimming.
//
// Exported behavior, not an implementation detail: it is part of what the API
// promises about the header, so it belongs in the documentation for anything
// that publishes one.
const maxRequestIDLen = 64

// safeRequestID permits the characters that appear in UUIDs, trace IDs, and
// similar correlation identifiers. Anything else is refused, because this value
// is echoed in a response header and written into logs — the two places where
// an attacker-controlled string does damage, through header injection and
// through log entries forged with embedded newlines.
var safeRequestID = regexp.MustCompile(`^[a-zA-Z0-9\-_.:]+$`)

// RequestID gives every request a correlation ID.
//
// A client-supplied X-Request-ID is used when it passes validation, so a
// caller's own trace ID survives into this service's logs. Otherwise one is
// generated. Either way the ID goes into the request context and comes back on
// the response, which is what lets a user quote an ID from an error body and
// have it locate the exact request in the log.
//
// An invalid client value is replaced rather than rejected: a correlation ID is
// never worth failing a request over.
func RequestID() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := sanitizeRequestID(r.Header.Get(requestid.Header))
			if id == "" {
				// rand.Text returns a 26-character base32 string from a
				// cryptographic source. A counter or timestamp would be
				// guessable, which matters here because the ID appears in error
				// bodies and support tickets.
				id = rand.Text()
			}

			w.Header().Set(requestid.Header, id)
			next.ServeHTTP(w, r.WithContext(requestid.NewContext(r.Context(), id)))
		})
	}
}

// sanitizeRequestID returns the client's ID if it is safe to echo, or an empty
// string if it is not.
func sanitizeRequestID(raw string) string {
	id := strings.TrimSpace(raw)
	if id == "" || len(id) > maxRequestIDLen || !safeRequestID.MatchString(id) {
		return ""
	}
	return id
}
