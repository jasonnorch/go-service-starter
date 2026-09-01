package respond_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jasonnorch/go-service-starter/internal/httpapi/respond"
	"github.com/jasonnorch/go-service-starter/internal/requestid"
)

func discard() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// body is the error envelope, redeclared here rather than exported from the
// package: this is the published wire contract, and a test that reuses the
// production struct cannot notice a field being renamed.
type body struct {
	Error struct {
		Code      string `json:"code"`
		Message   string `json:"message"`
		RequestID string `json:"request_id"`
	} `json:"error"`
}

func decodeBody(t *testing.T, rec *httptest.ResponseRecorder) body {
	t.Helper()
	var b body
	if err := json.Unmarshal(rec.Body.Bytes(), &b); err != nil {
		t.Fatalf("response is not valid JSON (%v): %s", err, rec.Body.String())
	}
	return b
}

func TestJSONWritesStatusAndContentType(t *testing.T) {
	rec := httptest.NewRecorder()
	respond.JSON(rec, discard(), http.StatusCreated, map[string]string{"id": "abc"})

	if rec.Code != http.StatusCreated {
		t.Errorf("status = %d, want 201", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q", ct)
	}
	if got := strings.TrimSpace(rec.Body.String()); got != `{"id":"abc"}` {
		t.Errorf("body = %s", got)
	}
}

// The reason JSON marshals into a buffer first: once the status line is
// written it cannot be taken back, so a value that fails to encode would
// otherwise leave the client with a 200 and a truncated body.
func TestJSONUnencodableValueStillReportsAnError(t *testing.T) {
	rec := httptest.NewRecorder()
	respond.JSON(rec, discard(), http.StatusOK, make(chan int))

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500 when the value cannot be encoded", rec.Code)
	}
	if decodeBody(t, rec).Error.Code != "internal_error" {
		t.Errorf("body = %s", rec.Body.String())
	}
}

func TestNoContent(t *testing.T) {
	rec := httptest.NewRecorder()
	respond.NoContent(rec)

	if rec.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("204 carried a body: %s", rec.Body.String())
	}
}

func TestFailUsesTheErrorsStatusAndCode(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/users/1", http.NoBody)

	respond.Fail(rec, req, discard(), respond.NotFound("no such user"))

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
	got := decodeBody(t, rec).Error
	if got.Code != "not_found" || got.Message != "no such user" {
		t.Errorf("error = %+v", got)
	}
}

// An unrecognized error must not borrow whichever status is nearest, and it
// must not repeat its own text to the client — that text is where connection
// strings and query fragments live.
func TestFailHidesUnrecognizedErrors(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/users/1", http.NoBody)
	secret := errors.New("dial tcp 10.0.0.5:5432: connection refused")

	respond.Fail(rec, req, discard(), secret)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "10.0.0.5") {
		t.Errorf("internal detail reached the client: %s", rec.Body.String())
	}
	if decodeBody(t, rec).Error.Code != "internal_error" {
		t.Errorf("body = %s", rec.Body.String())
	}
}

// The cause is the whole reason for the split: withheld from the client,
// required in the log, or a 500 is unactionable.
func TestFailLogsTheCauseOfServerErrors(t *testing.T) {
	var logged bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logged, &slog.HandlerOptions{Level: slog.LevelDebug}))
	req := httptest.NewRequest(http.MethodGet, "/users/1", http.NoBody)

	respond.Fail(httptest.NewRecorder(), req, logger, errors.New("connection refused to 10.0.0.5"))

	if !strings.Contains(logged.String(), "10.0.0.5") {
		t.Errorf("the cause was not logged, leaving the 500 unactionable: %s", logged.String())
	}
}

func TestFailIncludesTheRequestID(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/users/1", http.NoBody)
	req = req.WithContext(requestid.NewContext(req.Context(), "req-123"))

	respond.Fail(rec, req, discard(), respond.BadRequest("nope"))

	if got := decodeBody(t, rec).Error.RequestID; got != "req-123" {
		t.Errorf("request_id = %q, want req-123 — without it a user's report cannot be traced", got)
	}
}

// Omitted rather than sent empty, so a client cannot report an ID that is not
// one.
func TestFailOmitsAnAbsentRequestID(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/users/1", http.NoBody)

	respond.Fail(rec, req, discard(), respond.BadRequest("nope"))

	if strings.Contains(rec.Body.String(), "request_id") {
		t.Errorf("empty request_id was serialized: %s", rec.Body.String())
	}
}

// A handler wraps a domain error; the code that inspects it must still find it.
func TestErrorUnwrapsToItsCause(t *testing.T) {
	sentinel := errors.New("sentinel")
	err := respond.NotFound("no such user").Because(sentinel)

	if !errors.Is(err, sentinel) {
		t.Error("errors.Is could not reach the cause through the response error")
	}
	if got, ok := errors.AsType[*respond.Error](err); !ok || got.Status != http.StatusNotFound {
		t.Errorf("errors.AsType did not recover the response error: %v", err)
	}
}

// Because returns a copy, so a constructor's result cannot be mutated by one
// caller for every other.
func TestBecauseDoesNotMutateTheReceiver(t *testing.T) {
	base := respond.NotFound("missing")
	withCause := base.Because(errors.New("boom"))

	if base.Unwrap() != nil {
		t.Error("Because mutated the error it was called on")
	}
	if withCause.Unwrap() == nil {
		t.Error("Because did not attach the cause to the copy")
	}
}

func TestConstructorStatuses(t *testing.T) {
	tests := []struct {
		err  *respond.Error
		code string
		want int
	}{
		{respond.BadRequest("x"), "bad_request", http.StatusBadRequest},
		{respond.Unauthorized("x"), "unauthorized", http.StatusUnauthorized},
		{respond.Forbidden("x"), "forbidden", http.StatusForbidden},
		{respond.NotFound("x"), "not_found", http.StatusNotFound},
		{respond.Conflict("x"), "conflict", http.StatusConflict},
		{respond.UnsupportedMediaType("x"), "unsupported_media_type", http.StatusUnsupportedMediaType},
		{respond.PayloadTooLarge("x"), "payload_too_large", http.StatusRequestEntityTooLarge},
		{respond.Unprocessable("x"), "unprocessable_entity", http.StatusUnprocessableEntity},
		{respond.TooManyRequests("x"), "too_many_requests", http.StatusTooManyRequests},
	}

	for _, tt := range tests {
		t.Run(tt.code, func(t *testing.T) {
			if tt.err.Status != tt.want {
				t.Errorf("Status = %d, want %d", tt.err.Status, tt.want)
			}
			if tt.err.Code != tt.code {
				t.Errorf("Code = %q, want %q", tt.err.Code, tt.code)
			}
		})
	}
}
