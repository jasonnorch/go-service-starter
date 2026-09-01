package httpapi

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/jasonnorch/go-service-starter/internal/httpapi/respond"
)

// probedMethods are the methods Fallback tries when deciding whether a path
// exists under some other method. There is no way to ask a ServeMux what it has
// registered, so the set has to be enumerated; these are the ones a JSON API
// serves. Add to it if a route ever uses another.
var probedMethods = []string{
	http.MethodGet,
	http.MethodHead,
	http.MethodPost,
	http.MethodPut,
	http.MethodPatch,
	http.MethodDelete,
	http.MethodOptions,
}

// Fallback wraps a mux so that unmatched requests get the same JSON error
// envelope as everything else.
//
// Without it, the two most common failures in any API — a mistyped path and the
// wrong method — are the only responses that come back as plain text, from
// ServeMux's own built-in handlers. A client that parses every error body has
// to special-case exactly the two it will meet first.
//
// It also distinguishes the two, which matters: 404 says the path does not
// exist, and sends a developer looking for a typo; 405 says it does and the
// method is wrong, which is a different fix. The Allow header names the methods
// that would have worked.
func Fallback(mux *http.ServeMux, logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A non-empty pattern means a registered route matched, so the mux
		// handles it. Trailing-slash redirects report a pattern too and are
		// left to the mux, which is what should answer them.
		if _, pattern := mux.Handler(r); pattern != "" {
			mux.ServeHTTP(w, r)
			return
		}

		if allowed := allowedMethods(mux, r); len(allowed) > 0 {
			// RFC 9110 requires Allow on a 405, and it is what turns the
			// response into something a client can act on without the docs.
			w.Header().Set("Allow", strings.Join(allowed, ", "))
			respond.Fail(w, r, logger, respond.MethodNotAllowed(
				"method "+r.Method+" is not allowed on this path"))
			return
		}

		respond.Fail(w, r, logger, respond.NotFound("no route matches this path"))
	})
}

// allowedMethods returns the methods that would match r's path, by asking the
// mux the same question once per method. It runs only after a request has
// already failed to match, so the cost falls on the error path.
func allowedMethods(mux *http.ServeMux, r *http.Request) []string {
	var allowed []string

	for _, method := range probedMethods {
		if method == r.Method {
			continue
		}

		probe := r.Clone(r.Context())
		probe.Method = method
		if _, pattern := mux.Handler(probe); pattern != "" {
			allowed = append(allowed, method)
		}
	}

	return allowed
}
