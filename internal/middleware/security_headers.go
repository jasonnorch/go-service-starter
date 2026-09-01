package middleware

import "net/http"

// SecurityHeaders sets defensive response headers on every response.
//
// The values are chosen for a JSON API, which is the strictest case: it serves
// no markup, embeds nothing, and is never framed. A handler that serves HTML
// must override Content-Security-Policy with a policy for that page — these are
// defaults, and a default that cannot be overridden is a constraint.
//
// The headers are set before the handler runs, because a header written after
// the response has begun is silently discarded.
func SecurityHeaders() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()

			// Stops a browser from second-guessing Content-Type, which is what
			// turns a JSON response holding user input into executable markup.
			h.Set("X-Content-Type-Options", "nosniff")

			// frame-ancestors in the CSP supersedes X-Frame-Options in every
			// current browser; the header stays for older ones.
			h.Set("X-Frame-Options", "DENY")
			h.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")

			// API responses are per-caller and often authorized. Caching one in
			// a shared proxy is how one user's data reaches another.
			h.Set("Cache-Control", "no-store")

			// Keeps API paths and query strings out of the Referer header on
			// any navigation away, where they end up in a third party's logs.
			h.Set("Referrer-Policy", "no-referrer")

			// Two years, the minimum for preload-list eligibility. Browsers
			// ignore this over plain HTTP, so it is safe to set unconditionally
			// and correct as soon as the service is behind TLS.
			h.Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")

			next.ServeHTTP(w, r)
		})
	}
}
