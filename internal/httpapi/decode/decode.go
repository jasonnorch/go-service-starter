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
	"strings"

	"github.com/jasonnorch/go-service-starter/internal/httpapi/respond"
)

// Validator is implemented by request types that check themselves. When the
// decoded value implements it, JSON calls Validate and reports a failure as
// 422 — the request parsed, so it is not malformed; it is unacceptable.
//
// Defined here, where it is consumed, rather than in a shared types package: a
// request type satisfies it by having the method, and never has to import this
// package to do so.
type Validator interface {
	Validate() error
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

	if validator, ok := any(v).(Validator); ok {
		if err := validator.Validate(); err != nil {
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
