package httpapi

import (
	"log/slog"
	"net/http"
	"slices"
	"strings"

	"github.com/jasonnorch/go-service-starter/internal/httpapi/respond"
)

// Middleware wraps a handler in another handler.
//
// This one type is the reason the kit uses net/http directly. Every middleware
// written against this signature — in this repo, in the standard library, in
// any third-party package for the last decade — composes with every other one,
// and none of them has to be rewritten if the router is replaced. A framework's
// own middleware type buys convenience by giving that up: it composes only with
// itself, and everything written for it is stranded there.
type Middleware func(http.Handler) http.Handler

// Chain wraps h in mw, outermost first.
//
// Chain(h, A, B, C) runs A, then B, then C, then h — the order they are written
// in. Applying them in reverse is what produces that, and getting it backwards
// is the classic version of this bug: the recover middleware ends up inside the
// thing it was meant to protect.
func Chain(h http.Handler, mw ...Middleware) http.Handler {
	for _, m := range slices.Backward(mw) {
		h = m(h)
	}
	return h
}

// HandlerFunc is a handler that returns an error instead of writing one.
//
// This is the second half of the ergonomics, and it is what makes the central
// error mapping usable: a handler does
//
//	return respond.NotFound("no such user")
//
// and the response, the status, the log line, and the request ID all happen in
// one place. The alternative — writing the error and remembering to return —
// has a well-known failure where the return is forgotten and the handler
// carries on writing a second response onto the first.
type HandlerFunc func(http.ResponseWriter, *http.Request) error

// Group registers routes on a mux under a shared path prefix and middleware
// stack.
//
// It is about forty lines because that is all a prefix and a slice of
// middleware need. A group holds no routing logic of its own: patterns go
// straight to http.ServeMux, so Go 1.22 method matching and wildcards all work
// exactly as documented upstream, with nothing to relearn.
type Group struct {
	mux    *http.ServeMux
	logger *slog.Logger
	prefix string
	mw     []Middleware
}

// NewGroup returns a group registering on mux at the root with no middleware.
func NewGroup(mux *http.ServeMux, logger *slog.Logger) *Group {
	return &Group{mux: mux, logger: logger}
}

// Group returns a nested group: prefix is appended to this group's prefix, and
// mw runs after this group's middleware.
//
// The middleware slice is copied into a new allocation rather than built with
// append(g.mw, mw...). That form returns a slice sharing the parent's backing
// array whenever the parent has spare capacity, and a second child then
// overwrites the first child's middleware — a route silently running a stack it
// never asked for. Nothing here currently produces spare capacity, so the copy
// is what keeps that true of every future change rather than by luck.
func (g *Group) Group(prefix string, mw ...Middleware) *Group {
	combined := make([]Middleware, 0, len(g.mw)+len(mw))
	combined = append(combined, g.mw...)
	combined = append(combined, mw...)

	return &Group{
		mux:    g.mux,
		logger: g.logger,
		prefix: joinPrefix(g.prefix, prefix),
		mw:     combined,
	}
}

// Use returns a group with additional middleware and the same prefix.
func (g *Group) Use(mw ...Middleware) *Group { return g.Group("", mw...) }

// Handle registers an http.Handler. pattern is a standard ServeMux pattern,
// with or without a method: "GET /users/{id}" or "/users/{id}".
func (g *Group) Handle(pattern string, h http.Handler) {
	g.mux.Handle(g.pattern(pattern), Chain(h, g.mw...))
}

// HandleFunc registers an error-returning handler, sending anything it returns
// through respond.Fail.
func (g *Group) HandleFunc(pattern string, h HandlerFunc) {
	g.Handle(pattern, g.adapt(h))
}

// adapt turns a HandlerFunc into an http.Handler bound to the group's logger.
func (g *Group) adapt(h HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := h(w, r); err != nil {
			respond.Fail(w, r, g.logger, err)
		}
	})
}

// pattern applies the group's prefix to a ServeMux pattern, keeping any method
// where ServeMux expects it — at the front, not buried behind the prefix.
func (g *Group) pattern(pattern string) string {
	method, path := splitPattern(pattern)
	full := joinPrefix(g.prefix, path)
	if full == "" {
		full = "/"
	}
	if method == "" {
		return full
	}
	return method + " " + full
}

// splitPattern separates a leading method from the path. ServeMux patterns are
// "METHOD /path", "/path", or "METHOD host/path"; the method is whatever
// precedes the first space.
func splitPattern(pattern string) (method, path string) {
	if before, after, found := strings.Cut(pattern, " "); found {
		return before, strings.TrimSpace(after)
	}
	return "", pattern
}

// joinPrefix concatenates two path segments without producing "//", which
// ServeMux would treat as a distinct path and redirect.
func joinPrefix(base, next string) string {
	base = strings.TrimSuffix(base, "/")
	if next == "" {
		return base
	}
	if !strings.HasPrefix(next, "/") {
		next = "/" + next
	}
	return base + next
}
