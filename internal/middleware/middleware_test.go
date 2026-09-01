// The whole test file uses nothing but net/http, net/http/httptest and the
// standard library. That is the portability claim demonstrated rather than
// asserted: there is no engine to construct, no test context to fake, and no
// framework to import — so any of this middleware drops into any other
// stdlib-based service unchanged.
package middleware_test

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jasonnorch/go-service-starter/internal/middleware"
	"github.com/jasonnorch/go-service-starter/internal/requestid"
)

func discard() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func capture() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	return slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})), &buf
}

// ok is a handler that succeeds, for testing what surrounds it.
var ok = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
})

func serve(h http.Handler, r *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func get(path string) *http.Request {
	return httptest.NewRequest(http.MethodGet, path, http.NoBody)
}

// The behavior that justifies calling this middleware mandatory: without it,
// net/http drops the connection and the client sees a network error rather than
// a response.
func TestRecoverTurnsAPanicIntoA500(t *testing.T) {
	logger, logged := capture()
	panicky := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		panic("boom")
	})

	rec := serve(middleware.Recover(logger)(panicky), get("/boom"))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}

	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("panic response is not JSON: %s", rec.Body.String())
	}
	if body.Error.Code != "internal_error" {
		t.Errorf("code = %q, want internal_error", body.Error.Code)
	}
	// The panic value is server-side detail; the client gets the generic
	// message.
	if strings.Contains(body.Error.Message, "boom") {
		t.Errorf("the panic value reached the client: %s", rec.Body.String())
	}
	if !strings.Contains(logged.String(), "boom") {
		t.Errorf("the panic value was not logged: %s", logged.String())
	}
	// Without the stack, the log line says a panic happened and not where.
	if !strings.Contains(logged.String(), "stack") {
		t.Errorf("no stack in the panic log: %s", logged.String())
	}
}

// ErrAbortHandler is net/http's documented way to abandon a response
// deliberately. Converting it to a 500 would turn an intentional abort into a
// reported server error.
func TestRecoverRepanicsOnErrAbortHandler(t *testing.T) {
	aborting := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		panic(http.ErrAbortHandler)
	})
	h := middleware.Recover(discard())(aborting)

	defer func() {
		switch recovered := recover(); recovered {
		case nil:
			t.Error("ErrAbortHandler was swallowed instead of propagating")
		case http.ErrAbortHandler:
		default:
			t.Errorf("re-panicked with %v, want http.ErrAbortHandler", recovered)
		}
	}()

	h.ServeHTTP(httptest.NewRecorder(), get("/abort"))
}

// Once the response has begun, appending an error envelope produces something
// the client cannot parse. The recover must log and stop.
func TestRecoverDoesNotAppendToAStartedResponse(t *testing.T) {
	logger, logged := capture()
	partial := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"partial":true`))
		panic("boom")
	})

	// The logger's recorder is what lets Recover know the response started, so
	// the two are chained exactly as they are in production.
	h := middleware.RequestLogger(discard())(middleware.Recover(logger)(partial))
	rec := serve(h, get("/partial"))

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want the 200 that was already sent", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "internal_error") {
		t.Errorf("an error envelope was appended to a started response: %s", rec.Body.String())
	}
	if !strings.Contains(logged.String(), "boom") {
		t.Errorf("the panic was not logged: %s", logged.String())
	}
}

func TestRequestIDGeneratesOneWhenAbsent(t *testing.T) {
	var fromContext string
	h := middleware.RequestID()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fromContext = requestid.FromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	rec := serve(h, get("/"))

	if fromContext == "" {
		t.Error("no request ID reached the handler's context")
	}
	if got := rec.Header().Get(requestid.Header); got != fromContext {
		t.Errorf("response header = %q, context = %q; they must agree or a user cannot quote one", got, fromContext)
	}
}

// A caller's own trace ID surviving into this service's logs is the entire
// value of accepting the header.
func TestRequestIDAcceptsAValidClientValue(t *testing.T) {
	var fromContext string
	h := middleware.RequestID()(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		fromContext = requestid.FromContext(r.Context())
	}))

	req := get("/")
	req.Header.Set(requestid.Header, "01HQ8-trace.id:42")
	rec := serve(h, req)

	if fromContext != "01HQ8-trace.id:42" {
		t.Errorf("context ID = %q, want the client's value", fromContext)
	}
	if rec.Header().Get(requestid.Header) != "01HQ8-trace.id:42" {
		t.Errorf("the client's ID was not echoed back")
	}
}

// This value is echoed into a response header and written into logs, which are
// the two places an attacker-controlled string does damage.
func TestRequestIDRejectsUnsafeClientValues(t *testing.T) {
	tests := []struct {
		name string
		id   string
	}{
		{"header injection", "abc\r\nX-Admin: true"},
		{"log forging", "abc\ndeadbeef"},
		{"too long", strings.Repeat("a", 65)},
		{"spaces", "not a valid id"},
		{"whitespace only", "   "},
		{"non-ascii", "id-éé"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var fromContext string
			h := middleware.RequestID()(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				fromContext = requestid.FromContext(r.Context())
			}))

			req := get("/")
			req.Header.Set(requestid.Header, tt.id)
			rec := serve(h, req)

			if fromContext == strings.TrimSpace(tt.id) {
				t.Errorf("an unsafe client ID was used verbatim: %q", fromContext)
			}
			// Replaced, not rejected: a correlation ID is never worth failing a
			// request over.
			if fromContext == "" {
				t.Error("no replacement ID was generated")
			}
			if got := rec.Header().Get(requestid.Header); got != fromContext {
				t.Errorf("echoed %q but put %q in the context", got, fromContext)
			}
		})
	}
}

func TestSecurityHeaders(t *testing.T) {
	rec := serve(middleware.SecurityHeaders()(ok), get("/"))

	want := map[string]string{
		"X-Content-Type-Options":    "nosniff",
		"X-Frame-Options":           "DENY",
		"Cache-Control":             "no-store",
		"Referrer-Policy":           "no-referrer",
		"Content-Security-Policy":   "default-src 'none'; frame-ancestors 'none'",
		"Strict-Transport-Security": "max-age=63072000; includeSubDomains",
	}
	for header, value := range want {
		if got := rec.Header().Get(header); got != value {
			t.Errorf("%s = %q, want %q", header, got, value)
		}
	}
}

// Set before the handler runs, because a header written after the response has
// begun is silently discarded.
func TestSecurityHeadersSurviveAHandlerThatWritesImmediately(t *testing.T) {
	writer := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("early"))
	})

	rec := serve(middleware.SecurityHeaders()(writer), get("/"))

	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("headers were lost on a handler that wrote immediately")
	}
}

func TestBodyLimit(t *testing.T) {
	const limit = 32

	t.Run("oversized Content-Length is rejected before reading", func(t *testing.T) {
		var handlerRan bool
		h := middleware.BodyLimit(limit, discard())(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
			handlerRan = true
		}))

		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(strings.Repeat("a", 100)))
		rec := serve(h, req)

		if rec.Code != http.StatusRequestEntityTooLarge {
			t.Errorf("status = %d, want 413", rec.Code)
		}
		if handlerRan {
			t.Error("the handler ran on an oversized request")
		}
		if !strings.Contains(rec.Body.String(), "payload_too_large") {
			t.Errorf("body = %s", rec.Body.String())
		}
	})

	// Content-Length is a claim, not a fact, and it is absent entirely on a
	// chunked request — so the reader has to enforce the limit too.
	t.Run("an understated Content-Length is still caught", func(t *testing.T) {
		var readErr error
		h := middleware.BodyLimit(limit, discard())(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			_, readErr = io.ReadAll(r.Body)
		}))

		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(strings.Repeat("a", 100)))
		req.ContentLength = 4 // a lie
		serve(h, req)

		if readErr == nil {
			t.Fatal("reading past the limit succeeded; the limit was not enforced")
		}
		if _, isMax := readErr.(*http.MaxBytesError); !isMax {
			t.Errorf("read failed with %v, want *http.MaxBytesError", readErr)
		}
	})

	t.Run("a body within the limit passes through", func(t *testing.T) {
		var got string
		h := middleware.BodyLimit(limit, discard())(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			got = string(body)
		}))

		serve(h, httptest.NewRequest(http.MethodPost, "/", strings.NewReader("small")))

		if got != "small" {
			t.Errorf("body = %q, want %q", got, "small")
		}
	})
}

func TestRequestLoggerRecordsTheOutcome(t *testing.T) {
	logger, logged := capture()
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("hello"))
	})

	h := middleware.RequestID()(middleware.RequestLogger(logger)(handler))
	serve(h, httptest.NewRequest(http.MethodPost, "/users", http.NoBody))

	var line map[string]any
	if err := json.Unmarshal(logged.Bytes(), &line); err != nil {
		t.Fatalf("log line is not JSON (%v): %s", err, logged.String())
	}

	if line["method"] != http.MethodPost {
		t.Errorf("method = %v", line["method"])
	}
	if line["path"] != "/users" {
		t.Errorf("path = %v", line["path"])
	}
	if line["status"] != float64(http.StatusCreated) {
		t.Errorf("status = %v, want 201", line["status"])
	}
	if line["bytes"] != float64(5) {
		t.Errorf("bytes = %v, want 5", line["bytes"])
	}
	if line["request_id"] == "" || line["request_id"] == nil {
		t.Error("no request_id on the log line, so it cannot be correlated with an error the user reports")
	}
	if _, present := line["duration"]; !present {
		t.Error("no duration on the log line")
	}
}

// A wall of 404s must not sit at the same level as a server error that should
// page someone.
func TestRequestLoggerLevelsByStatus(t *testing.T) {
	tests := []struct {
		status int
		want   string
	}{
		{http.StatusOK, "INFO"},
		{http.StatusNotFound, "WARN"},
		{http.StatusInternalServerError, "ERROR"},
	}

	for _, tt := range tests {
		t.Run(http.StatusText(tt.status), func(t *testing.T) {
			logger, logged := capture()
			handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
			})

			serve(middleware.RequestLogger(logger)(handler), get("/"))

			var line map[string]any
			if err := json.Unmarshal(logged.Bytes(), &line); err != nil {
				t.Fatalf("log line is not JSON: %s", logged.String())
			}
			if line["level"] != tt.want {
				t.Errorf("level = %v, want %s", line["level"], tt.want)
			}
		})
	}
}

// A handler that writes a body without calling WriteHeader gets a 200 from
// net/http, and the log has to say the same thing the client received.
func TestRequestLoggerDefaultsToTheStatusNetHTTPSends(t *testing.T) {
	logger, logged := capture()
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("implicit 200"))
	})

	serve(middleware.RequestLogger(logger)(handler), get("/"))

	var line map[string]any
	if err := json.Unmarshal(logged.Bytes(), &line); err != nil {
		t.Fatalf("log line is not JSON: %s", logged.String())
	}
	if line["status"] != float64(http.StatusOK) {
		t.Errorf("status = %v, want 200", line["status"])
	}
}

// Wrapping a ResponseWriter silently disables streaming unless the wrapper
// exposes the original — the classic cost of access-log middleware.
func TestRequestLoggerKeepsTheResponseControllerWorking(t *testing.T) {
	var flushErr error
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("chunk"))
		flushErr = http.NewResponseController(w).Flush()
	})

	serve(middleware.RequestLogger(discard())(handler), get("/stream"))

	if flushErr != nil {
		t.Errorf("Flush through the wrapper failed: %v — streaming responses are broken", flushErr)
	}
}
