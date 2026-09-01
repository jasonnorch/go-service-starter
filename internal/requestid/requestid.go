// Package requestid carries a request's correlation ID through its context.
//
// It exists as its own package to break an import cycle rather than for its own
// sake: the middleware that assigns the ID and the responder that reports it in
// an error body both need it, and the middleware needs the responder. A leaf
// package everything can depend on is the seam that makes that work.
package requestid

import "context"

// Header is the HTTP header the ID is read from and echoed back on.
const Header = "X-Request-ID"

// ctxKey is unexported so no other package can collide with it, which is the
// reason context keys are never plain strings.
type ctxKey struct{}

// NewContext returns ctx carrying id.
func NewContext(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ctxKey{}, id)
}

// FromContext returns the request's ID, or an empty string when none was set.
//
// The empty return is deliberate: a missing correlation ID is never worth
// failing a request over, and every caller would otherwise write the same
// discard of the second return value.
func FromContext(ctx context.Context) string {
	id, _ := ctx.Value(ctxKey{}).(string)
	return id
}
