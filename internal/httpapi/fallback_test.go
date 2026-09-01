package httpapi_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/jasonnorch/go-service-starter/internal/httpapi"
)

func fallbackMux() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /users", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"users":[]}`))
	})
	mux.HandleFunc("POST /users", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
	})
	mux.HandleFunc("GET /users/{id}", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"id":"1"}`))
	})
	return httpapi.Fallback(mux, discardLogger())
}

func request(t *testing.T, h http.Handler, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, http.NoBody))
	return rec
}

func errorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not JSON (%v): %s", err, rec.Body.String())
	}
	return body.Error.Code
}

func TestFallbackPassesMatchedRoutesThrough(t *testing.T) {
	h := fallbackMux()

	for _, tc := range []struct {
		method, path string
		want         int
	}{
		{http.MethodGet, "/users", http.StatusOK},
		{http.MethodPost, "/users", http.StatusCreated},
		{http.MethodGet, "/users/42", http.StatusOK},
	} {
		rec := request(t, h, tc.method, tc.path)
		if rec.Code != tc.want {
			t.Errorf("%s %s = %d, want %d", tc.method, tc.path, rec.Code, tc.want)
		}
	}
}

// A mistyped path is the first failure any client meets, and it must not be the
// one response that is not JSON.
func TestFallbackUnknownPathIsAJSON404(t *testing.T) {
	rec := request(t, fallbackMux(), http.MethodGet, "/nope")

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want JSON", ct)
	}
	if got := errorCode(t, rec); got != "not_found" {
		t.Errorf("code = %q, want not_found", got)
	}
}

// 405 rather than 404, because the two send a developer looking in different
// places: a typo in the path, or the wrong verb on a path that exists.
func TestFallbackWrongMethodIsAJSON405WithAllow(t *testing.T) {
	rec := request(t, fallbackMux(), http.MethodDelete, "/users")

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
	if got := errorCode(t, rec); got != "method_not_allowed" {
		t.Errorf("code = %q, want method_not_allowed", got)
	}

	allowed := strings.Split(rec.Header().Get("Allow"), ", ")
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		if !slices.Contains(allowed, method) {
			t.Errorf("Allow = %q, missing %s", rec.Header().Get("Allow"), method)
		}
	}
	if slices.Contains(allowed, http.MethodDelete) {
		t.Errorf("Allow = %q lists the method that was just refused", rec.Header().Get("Allow"))
	}
}

// The distinction has to survive a wildcard, which is where a hand-rolled
// version of this usually stops working.
func TestFallbackDistinguishes405From404OnWildcardRoutes(t *testing.T) {
	h := fallbackMux()

	if rec := request(t, h, http.MethodDelete, "/users/42"); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("DELETE /users/42 = %d, want 405 — the path exists under GET", rec.Code)
	}
	if rec := request(t, h, http.MethodGet, "/orders/42"); rec.Code != http.StatusNotFound {
		t.Errorf("GET /orders/42 = %d, want 404", rec.Code)
	}
}

// ServeMux answers HEAD from a GET route itself, and the fallback must not get
// in the way of that.
func TestFallbackLeavesHeadToTheMux(t *testing.T) {
	rec := request(t, fallbackMux(), http.MethodHead, "/users")

	if rec.Code != http.StatusOK {
		t.Errorf("HEAD /users = %d, want 200", rec.Code)
	}
}
