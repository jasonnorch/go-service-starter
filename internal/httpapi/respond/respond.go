// Package respond writes HTTP responses.
//
// It is the single place that decides how a response looks: what the
// content-type is, what an error body contains, and what reaches the client
// versus what reaches the log. Handlers that each assemble their own body drift
// apart one endpoint at a time, and the drift is only ever noticed by whoever
// is writing the client.
package respond

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/jasonnorch/go-service-starter/internal/requestid"
)

// errorBody is the response every failure produces. Nesting under "error"
// leaves room for a success payload to use the top level, so a client can tell
// the two apart structurally rather than by checking the status twice.
type errorBody struct {
	Error errorDetail `json:"error"`
}

type errorDetail struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id,omitempty"`
}

// JSON writes v as the response body with the given status.
//
// v is marshaled into a buffer before anything is written, because the status
// line cannot be taken back. Encoding straight to the ResponseWriter means a
// value that fails to marshal — an unsupported type, a cyclic structure — has
// already sent 200 and some prefix of a body, leaving the client to parse
// truncated JSON with no indication anything went wrong.
func JSON(w http.ResponseWriter, logger *slog.Logger, status int, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		logger.Error("encoding response failed", "error", err, "status", status)
		writeJSON(w, http.StatusInternalServerError, mustMarshal(errorBody{errorDetail{
			Code:    "internal_error",
			Message: "an internal error occurred",
		}}))
		return
	}
	writeJSON(w, status, body)
}

// NoContent writes 204 with no body, for a request that succeeded and has
// nothing to say.
func NoContent(w http.ResponseWriter) {
	w.WriteHeader(http.StatusNoContent)
}

// Fail writes err as a JSON error response. It is named Fail rather than Error
// because the type an error is mapped to already owns that name in this
// package, and a function and a type cannot share one.
//
// Server errors are logged with their internal cause; client errors are logged
// at debug, because a 404 is the client's problem and a log line per occurrence
// is how a log stops being read. Neither is logged with the request's method
// and path — the request logger middleware already records those, and repeating
// them here doubles the volume of every failure.
func Fail(w http.ResponseWriter, r *http.Request, logger *slog.Logger, err error) {
	e := asError(err)

	attrs := []any{"code", e.Code, "status", e.Status}
	if cause := e.Unwrap(); cause != nil {
		attrs = append(attrs, "error", cause)
	}
	if e.Status >= http.StatusInternalServerError {
		logger.ErrorContext(r.Context(), e.Message, attrs...)
	} else {
		logger.DebugContext(r.Context(), e.Message, attrs...)
	}

	body := errorBody{errorDetail{
		Code:      e.Code,
		Message:   e.Message,
		RequestID: requestid.FromContext(r.Context()),
	}}
	writeJSON(w, e.Status, mustMarshal(body))
}

// writeJSON sends an already-marshaled body. Content-Type is set before
// WriteHeader; setting a header afterwards is a silent no-op.
func writeJSON(w http.ResponseWriter, status int, body []byte) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	// The write can only fail if the client has gone, which nothing here can
	// act on — and the status has already been sent, so there is no error
	// response left to send.
	_, _ = w.Write(append(body, '\n'))
}

// mustMarshal encodes a value that cannot fail to encode.
//
// It is used only for errorBody, which is four strings. If that ever stops
// being true the fallback is an empty body rather than a panic: a marshaling
// bug should not take the process down while it is already reporting a failure.
func mustMarshal(v any) []byte {
	body, err := json.Marshal(v)
	if err != nil {
		return bytes.TrimSpace([]byte(`{"error":{"code":"internal_error","message":"an internal error occurred"}}`))
	}
	return body
}
