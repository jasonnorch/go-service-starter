package decode_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jasonnorch/go-service-starter/internal/httpapi/decode"
	"github.com/jasonnorch/go-service-starter/internal/httpapi/respond"
)

type user struct {
	Name string `json:"name"`
	Age  int    `json:"age"`
}

// validated implements decode.Validator by having the method — it never imports
// the interface, which is the point of defining it at the consumer.
type validated struct {
	Name string `json:"name"`
}

func (v validated) Validate() error {
	if v.Name == "" {
		return errors.New("name is required")
	}
	return nil
}

const oneMiB = 1 << 20

func post(body string) (*httptest.ResponseRecorder, *http.Request) {
	req := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return httptest.NewRecorder(), req
}

// assertStatus checks that err is a *respond.Error with the expected status,
// which is what determines the response a client actually sees.
func assertStatus(t *testing.T, err error, want int) *respond.Error {
	t.Helper()
	if err == nil {
		t.Fatalf("expected an error with status %d, got nil", want)
	}
	e, ok := errors.AsType[*respond.Error](err)
	if !ok {
		t.Fatalf("error is not a *respond.Error, so it would be reported as a 500: %v", err)
	}
	if e.Status != want {
		t.Errorf("status = %d, want %d (%v)", e.Status, want, err)
	}
	return e
}

func TestJSONDecodesAValidBody(t *testing.T) {
	w, r := post(`{"name":"ada","age":36}`)

	got, err := decode.JSON[user](w, r, oneMiB)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Name != "ada" || got.Age != 36 {
		t.Errorf("decoded %+v", got)
	}
}

func TestJSONRejectsBadBodies(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		wantStatus  int
		wantMessage string
	}{
		{
			name:        "empty body",
			body:        "",
			wantStatus:  http.StatusBadRequest,
			wantMessage: "empty",
		},
		{
			name:       "malformed JSON reports where",
			body:       `{"name":"ada",}`,
			wantStatus: http.StatusBadRequest,
			// The position is what makes the message actionable.
			wantMessage: "position",
		},
		{
			name:        "truncated JSON",
			body:        `{"name":"ada"`,
			wantStatus:  http.StatusBadRequest,
			wantMessage: "truncated",
		},
		{
			// A typo that silently decodes to the zero value is a request that
			// appears to succeed while doing something else.
			name:        "unknown field is named",
			body:        `{"full_name":"ada"}`,
			wantStatus:  http.StatusBadRequest,
			wantMessage: "full_name",
		},
		{
			name:        "wrong type names the field",
			body:        `{"name":"ada","age":"thirty"}`,
			wantStatus:  http.StatusBadRequest,
			wantMessage: "age",
		},
		{
			name:        "trailing value",
			body:        `{"name":"ada"}{"name":"grace"}`,
			wantStatus:  http.StatusBadRequest,
			wantMessage: "single JSON object",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w, r := post(tt.body)

			_, err := decode.JSON[user](w, r, oneMiB)
			e := assertStatus(t, err, tt.wantStatus)
			if !strings.Contains(e.Message, tt.wantMessage) {
				t.Errorf("message %q does not mention %q", e.Message, tt.wantMessage)
			}
		})
	}
}

// Valid JSON, just too much of it — a 413, not a 400.
func TestJSONEnforcesTheSizeLimit(t *testing.T) {
	w, r := post(`{"name":"` + strings.Repeat("a", 500) + `"}`)

	_, err := decode.JSON[user](w, r, 64)
	e := assertStatus(t, err, http.StatusRequestEntityTooLarge)
	if !strings.Contains(e.Message, "64") {
		t.Errorf("message %q does not state the limit", e.Message)
	}
}

func TestJSONContentType(t *testing.T) {
	t.Run("wrong type is rejected", func(t *testing.T) {
		w, r := post(`{"name":"ada"}`)
		r.Header.Set("Content-Type", "application/xml")

		_, err := decode.JSON[user](w, r, oneMiB)
		assertStatus(t, err, http.StatusUnsupportedMediaType)
	})

	t.Run("charset parameter is accepted", func(t *testing.T) {
		w, r := post(`{"name":"ada"}`)
		r.Header.Set("Content-Type", "application/json; charset=utf-8")

		if _, err := decode.JSON[user](w, r, oneMiB); err != nil {
			t.Errorf("a charset parameter was rejected: %v", err)
		}
	})

	t.Run("+json suffix is accepted", func(t *testing.T) {
		w, r := post(`{"name":"ada"}`)
		r.Header.Set("Content-Type", "application/merge-patch+json")

		if _, err := decode.JSON[user](w, r, oneMiB); err != nil {
			t.Errorf("a +json media type was rejected: %v", err)
		}
	})

	// Rejecting a correctly encoded body for a missing header is pedantry
	// rather than safety.
	t.Run("absent header is accepted", func(t *testing.T) {
		w, r := post(`{"name":"ada"}`)
		r.Header.Del("Content-Type")

		if _, err := decode.JSON[user](w, r, oneMiB); err != nil {
			t.Errorf("a body with no Content-Type was rejected: %v", err)
		}
	})
}

// Parsed but unacceptable is 422, not 400 — the distinction tells a client
// whether to fix its encoding or its data.
func TestJSONRunsValidate(t *testing.T) {
	t.Run("failure is unprocessable", func(t *testing.T) {
		w, r := post(`{"name":""}`)

		_, err := decode.JSON[validated](w, r, oneMiB)
		e := assertStatus(t, err, http.StatusUnprocessableEntity)
		if !strings.Contains(e.Message, "name is required") {
			t.Errorf("message %q does not carry the validation failure", e.Message)
		}
	})

	t.Run("success passes through", func(t *testing.T) {
		w, r := post(`{"name":"ada"}`)

		got, err := decode.JSON[validated](w, r, oneMiB)
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		if got.Name != "ada" {
			t.Errorf("decoded %+v", got)
		}
	})
}
