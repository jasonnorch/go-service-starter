// Package decode reads and validates JSON request bodies.
//
// Every failure it can produce comes back as a *respond.Error carrying a status
// and a message a client can act on. The alternative — returning the raw
// json.SyntaxError — either leaks internal detail or, more often, becomes a
// bare 400 with no body, which is the least useful thing an API can say.
package decode

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"reflect"
	"strings"

	"github.com/jasonnorch/go-service-starter/internal/httpapi/respond"
)

// Validator is implemented by request types that check themselves. When the
// decoded value implements it, JSON calls Validate and reports a failure as
// 422 — the request parsed, so it is not malformed; it is unacceptable.
//
// Either receiver works: JSON checks the value and a pointer to it, so
// `func (r Request) Validate()` and `func (r *Request) Validate()` both take
// effect for `JSON[Request]`.
//
// # The returned text is sent to the client
//
// Validate's error message is published verbatim as the response body's
// message, because that is the entire value of a 422: telling the caller which
// field is wrong so they can fix it. A fixed, generic message would make this
// interface pointless.
//
// That makes the message a public API surface, and it constrains what may go
// in it:
//
//	func (r CreateUser) Validate() error {
//	    if r.Email == "" {
//	        return errors.New("email is required")     // good: about the input
//	    }
//	    if err := r.checkUniqueness(); err != nil {
//	        return fmt.Errorf("checking uniqueness: %w", err)   // WRONG: publishes
//	    }                                                       // internal detail
//	    return nil
//	}
//
// Validate is for checking the shape of what was sent — required fields,
// ranges, formats — using only the request's own contents. Anything that
// consults a database, an upstream service, or process state belongs in the
// handler, where its failure becomes a 500 with the cause logged rather than a
// 422 with the cause published.
//
// Defined here, where it is consumed, rather than in a shared types package: a
// request type satisfies it by having the method, and never has to import this
// package to do so.
type Validator interface {
	Validate() error
}

// isNil reports whether the decoded value is a nil of a nilable kind.
//
// It cannot be written as `any(v) == nil`: a nil *Request stored in an
// interface is a non-nil interface holding a typed nil, which compares unequal
// to nil and still satisfies any interface its method set covers. That is the
// whole reason the panic this guards is possible.
//
// Only pointer T is affected. A value T decodes `null` into its zero value,
// which is a legitimate struct that Validate — if the type has one — judges on
// its merits like any other input.
func isNil[T any](v T) bool {
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Invalid:
		// The interface itself was nil, which only happens for an interface T.
		return true
	case reflect.Pointer, reflect.Map, reflect.Slice, reflect.Interface, reflect.Func, reflect.Chan:
		return rv.IsNil()
	default:
		return false
	}
}

// validatorFor returns v as a Validator, whichever receiver Validate was
// declared with.
//
// The pointer check is not a nicety. A pointer-receiver Validate is the more
// common Go spelling, and it is absent from T's method set — so checking only
// the value would leave `JSON[Request]` silently skipping validation for
// `func (*Request) Validate()`. Silence is the whole problem: nothing fails,
// nothing logs, and unvalidated data reaches the handler looking checked.
//
// Both checks are needed rather than just the pointer one, because T may
// already be a pointer type, and a **Request has no methods at all.
func validatorFor[T any](v *T) (Validator, bool) {
	if validator, ok := any(*v).(Validator); ok {
		return validator, true
	}
	validator, ok := any(v).(Validator)
	return validator, ok
}

// JSON reads the request body into a T.
//
// maxBytes bounds the body. It is passed explicitly rather than read from
// configuration so an endpoint that should accept far less than the global
// limit can say so, and because a decoder that silently depends on middleware
// having run is a decoder that has no limit the first time it is used without
// it.
//
// The ResponseWriter is required by http.MaxBytesReader, which uses it to mark
// the connection for closing — without that, a client that keeps sending after
// the limit is hit leaves the connection in an undefined state.
func JSON[T any](w http.ResponseWriter, r *http.Request, maxBytes int64) (T, error) {
	var v T

	if err := requireJSONContentType(r); err != nil {
		return v, err
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)

	dec := json.NewDecoder(r.Body)
	// An unknown field is nearly always a typo or a client written against a
	// different version, and silently dropping it means the request appears to
	// succeed while doing something other than what was asked.
	dec.DisallowUnknownFields()

	if err := dec.Decode(&v); err != nil {
		return v, decodeError(err)
	}

	// A second value in the body means the client sent something other than the
	// single object the endpoint documents, and ignoring the remainder hides
	// that.
	if err := dec.Decode(new(json.RawMessage)); !errors.Is(err, io.EOF) {
		return v, respond.BadRequest("body must contain a single JSON object")
	}

	// A body of `null` is valid JSON and decodes into a pointer T as nil. Left
	// alone it reaches the handler as a nil pointer to be dereferenced there,
	// and reaches Validate first — where a pointer receiver dereferences it and
	// panics, since a typed nil still satisfies the interface. Either way one
	// client sending four characters produces a crash rather than a response.
	if isNil(v) {
		return v, respond.BadRequest("request body must be a JSON object, not null")
	}

	if validator, ok := validatorFor(&v); ok {
		if err := validator.Validate(); err != nil {
			// The message is published to the client deliberately — see the
			// contract on Validator, which is what keeps that safe.
			return v, respond.Unprocessable(err.Error()).Because(err)
		}
	}

	return v, nil
}

// requireJSONContentType rejects a body sent as something other than JSON.
// Absent is treated as JSON: plenty of clients omit the header on a request
// they nonetheless encoded correctly, and rejecting those is pedantry rather
// than safety.
func requireJSONContentType(r *http.Request) error {
	raw := r.Header.Get("Content-Type")
	if raw == "" {
		return nil
	}
	mediaType, _, err := mime.ParseMediaType(raw)
	if err != nil {
		return respond.BadRequest("malformed Content-Type header").Because(err)
	}
	if mediaType != "application/json" && !strings.HasSuffix(mediaType, "+json") {
		return respond.UnsupportedMediaType(
			fmt.Sprintf("expected application/json, got %s", mediaType))
	}
	return nil
}

// decodeError turns encoding/json's errors into responses that say what to fix.
func decodeError(err error) error {
	switch {
	case errors.Is(err, io.EOF):
		return respond.BadRequest("request body is empty").Because(err)

	case errors.Is(err, io.ErrUnexpectedEOF):
		return respond.BadRequest("request body contains truncated JSON").Because(err)
	}

	// MaxBytesReader reports the limit being hit, which is a 413 rather than a
	// malformed body — the client sent valid JSON, just too much of it.
	if maxErr, ok := errors.AsType[*http.MaxBytesError](err); ok {
		return respond.PayloadTooLarge(
			fmt.Sprintf("request body must not exceed %d bytes", maxErr.Limit)).Because(err)
	}

	// The offset is what makes a syntax error actionable, so it is reported
	// even though the message it comes with is not.
	if syntaxErr, ok := errors.AsType[*json.SyntaxError](err); ok {
		return respond.BadRequest(
			fmt.Sprintf("request body contains malformed JSON at position %d", syntaxErr.Offset),
		).Because(err)
	}

	// Naming both the field and the type it should have is the difference
	// between a client fixing this in a minute and reading the source.
	if typeErr, ok := errors.AsType[*json.UnmarshalTypeError](err); ok {
		if typeErr.Field != "" {
			return respond.BadRequest(
				fmt.Sprintf("field %q must be of type %s", typeErr.Field, typeErr.Type),
			).Because(err)
		}
		return respond.BadRequest(
			fmt.Sprintf("request body must be of type %s", typeErr.Type)).Because(err)
	}

	// DisallowUnknownFields reports through a plain error with no type to match
	// on, so the prefix is the only handle available. Matching it is fragile;
	// the fallback below is what keeps that fragility from mattering.
	const unknownFieldPrefix = "json: unknown field "
	if after, found := strings.CutPrefix(err.Error(), unknownFieldPrefix); found {
		return respond.BadRequest(fmt.Sprintf("unknown field %s", after)).Because(err)
	}

	return respond.BadRequest("request body is not valid JSON").Because(err)
}
