package respond

import (
	"errors"
	"fmt"
	"net/http"
)

// Error is an error that knows what HTTP response it should become.
//
// It separates the two audiences an error has. Message and Code go to the
// client and must be safe to publish; cause stays server-side and carries the
// detail — the failing query, the upstream error — that belongs in a log and
// nowhere else. Returning err.Error() to a caller is how database schemas and
// internal hostnames end up in someone else's console.
type Error struct {
	// Status is the HTTP status code to send.
	Status int
	// Code is a stable, machine-readable identifier. Clients switch on this;
	// they should never have to match on Message, which is free to be reworded.
	Code string
	// Message is human-readable and safe for the client.
	Message string

	cause error
}

// Error implements the error interface.
func (e *Error) Error() string {
	if e.cause != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.cause)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// Unwrap exposes the internal cause to errors.Is and errors.As, so a handler
// can wrap a domain error without hiding it from the code that inspects it.
func (e *Error) Unwrap() error { return e.cause }

// Because returns a copy of e carrying err as its internal cause. The cause is
// logged, never sent:
//
//	return respond.NotFound("no such user").Because(err)
func (e *Error) Because(err error) *Error {
	clone := *e
	clone.cause = err
	return &clone
}

// New builds an Error with an explicit status and code. Prefer the named
// constructors below; this is for statuses they do not cover.
func New(status int, code, message string) *Error {
	return &Error{Status: status, Code: code, Message: message}
}

// The named constructors. Each fixes the status and code so that a given
// failure looks identical everywhere it occurs — the thing that goes wrong when
// every handler assembles its own error body.
func BadRequest(message string) *Error {
	return New(http.StatusBadRequest, "bad_request", message)
}

func Unauthorized(message string) *Error {
	return New(http.StatusUnauthorized, "unauthorized", message)
}

func Forbidden(message string) *Error {
	return New(http.StatusForbidden, "forbidden", message)
}

func NotFound(message string) *Error {
	return New(http.StatusNotFound, "not_found", message)
}

// MethodNotAllowed must be sent with an Allow header naming the methods that
// would have worked; RFC 9110 requires it, and without it the response says
// only that the client is wrong.
func MethodNotAllowed(message string) *Error {
	return New(http.StatusMethodNotAllowed, "method_not_allowed", message)
}

func Conflict(message string) *Error {
	return New(http.StatusConflict, "conflict", message)
}

func UnsupportedMediaType(message string) *Error {
	return New(http.StatusUnsupportedMediaType, "unsupported_media_type", message)
}

func PayloadTooLarge(message string) *Error {
	return New(http.StatusRequestEntityTooLarge, "payload_too_large", message)
}

func Unprocessable(message string) *Error {
	return New(http.StatusUnprocessableEntity, "unprocessable_entity", message)
}

func TooManyRequests(message string) *Error {
	return New(http.StatusTooManyRequests, "too_many_requests", message)
}

// Internal is the response for anything the client cannot act on. Its message
// is fixed and uninformative on purpose; err carries the detail to the log.
func Internal(err error) *Error {
	return New(http.StatusInternalServerError, "internal_error",
		"an internal error occurred").Because(err)
}

// asError converts any error into the Error to send.
//
// This is the one place a failure becomes a status code. Handlers return
// whatever error they have, and the mapping happens here rather than as an
// errors.Is ladder repeated in every handler that can hit the same failure.
//
// To map a domain error, add a case:
//
//	switch {
//	case errors.Is(err, store.ErrNotFound):
//	    return NotFound("resource not found").Because(err)
//	case errors.Is(err, store.ErrDuplicate):
//	    return Conflict("already exists").Because(err)
//	}
//
// Anything unrecognized becomes a 500. That default is deliberate: a new error
// type should announce itself as a server-side gap rather than quietly
// borrowing whichever status happens to be nearest.
func asError(err error) *Error {
	if e, ok := errors.AsType[*Error](err); ok {
		return e
	}
	return Internal(err)
}
