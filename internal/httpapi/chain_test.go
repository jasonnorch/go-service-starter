package httpapi_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jasonnorch/go-service-starter/internal/httpapi"
	"github.com/jasonnorch/go-service-starter/internal/httpapi/respond"
)

// tag returns a middleware that records that it ran, in order.
func tag(order *[]string, name string) httpapi.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			*order = append(*order, name)
			next.ServeHTTP(w, r)
		})
	}
}

func okHandler(order *[]string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		*order = append(*order, "handler")
		w.WriteHeader(http.StatusOK)
	})
}

// Getting this backwards is the classic version of the bug: the recover
// middleware ends up inside the thing it was meant to protect.
func TestChainRunsMiddlewareInWrittenOrder(t *testing.T) {
	var order []string
	h := httpapi.Chain(okHandler(&order), tag(&order, "a"), tag(&order, "b"), tag(&order, "c"))

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", http.NoBody))

	if got := strings.Join(order, ","); got != "a,b,c,handler" {
		t.Errorf("order = %s, want a,b,c,handler", got)
	}
}

func TestChainWithNoMiddlewareReturnsTheHandler(t *testing.T) {
	var order []string
	rec := httptest.NewRecorder()

	httpapi.Chain(okHandler(&order)).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", http.NoBody))

	if rec.Code != http.StatusOK || len(order) != 1 {
		t.Errorf("status = %d, ran = %v", rec.Code, order)
	}
}

func TestGroupPrefixesRoutes(t *testing.T) {
	tests := []struct {
		name       string
		prefix     string
		pattern    string
		request    string
		method     string
		wantStatus int
	}{
		{"method is kept at the front", "/v1", "GET /users", "/v1/users", http.MethodGet, http.StatusOK},
		{"wrong method still 405s", "/v1", "GET /users", "/v1/users", http.MethodPost, http.StatusMethodNotAllowed},
		{"pattern without a method", "/v1", "/health", "/v1/health", http.MethodGet, http.StatusOK},
		{"wildcards survive prefixing", "/v1", "GET /users/{id}", "/v1/users/42", http.MethodGet, http.StatusOK},
		{"trailing slash on the prefix", "/v1/", "GET /users", "/v1/users", http.MethodGet, http.StatusOK},
		{"pattern with no leading slash", "/v1", "GET users", "/v1/users", http.MethodGet, http.StatusOK},
		{"empty prefix", "", "GET /users", "/users", http.MethodGet, http.StatusOK},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var order []string
			mux := http.NewServeMux()
			httpapi.NewGroup(mux, discardLogger()).Group(tt.prefix).Handle(tt.pattern, okHandler(&order))

			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, httptest.NewRequest(tt.method, tt.request, http.NoBody))

			if rec.Code != tt.wantStatus {
				t.Errorf("%s %s = %d, want %d", tt.method, tt.request, rec.Code, tt.wantStatus)
			}
		})
	}
}

func TestGroupNestsPrefixesAndMiddleware(t *testing.T) {
	var order []string
	mux := http.NewServeMux()

	root := httpapi.NewGroup(mux, discardLogger()).Use(tag(&order, "root"))
	v1 := root.Group("/v1", tag(&order, "v1"))
	admin := v1.Group("/admin", tag(&order, "admin"))
	admin.Handle("GET /stats", okHandler(&order))

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/admin/stats", http.NoBody))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 — the nested prefix did not resolve", rec.Code)
	}
	if got := strings.Join(order, ","); got != "root,v1,admin,handler" {
		t.Errorf("order = %s, want root,v1,admin,handler", got)
	}
}

// Sibling groups are independent: middleware added to one must not reach the
// other. This asserts the behavior, not the mechanism — the aliasing bug that
// Group's explicit slice copy rules out depends on spare capacity that the
// public API never produces, so no test can reach it.
func TestSiblingGroupsDoNotShareMiddleware(t *testing.T) {
	var order []string
	mux := http.NewServeMux()

	parent := httpapi.NewGroup(mux, discardLogger()).Use(tag(&order, "parent"))
	parent.Group("/a", tag(&order, "only-a")).Handle("GET /x", okHandler(&order))
	parent.Group("/b", tag(&order, "only-b")).Handle("GET /x", okHandler(&order))

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/b/x", http.NoBody))

	if got := strings.Join(order, ","); got != "parent,only-b,handler" {
		t.Errorf("order = %s, want parent,only-b,handler — group b ran group a's middleware", got)
	}
}

func TestHandleFuncSendsReturnedErrorsThroughRespond(t *testing.T) {
	mux := http.NewServeMux()
	httpapi.NewGroup(mux, discardLogger()).HandleFunc("GET /users/{id}",
		func(_ http.ResponseWriter, _ *http.Request) error {
			return respond.NotFound("no such user")
		})

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/users/42", http.NoBody))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	var got struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("response is not JSON: %s", rec.Body.String())
	}
	if got.Error.Code != "not_found" {
		t.Errorf("code = %q, want not_found", got.Error.Code)
	}
}

func TestHandleFuncWritesNothingExtraOnSuccess(t *testing.T) {
	mux := http.NewServeMux()
	httpapi.NewGroup(mux, discardLogger()).HandleFunc("GET /ok",
		func(w http.ResponseWriter, _ *http.Request) error {
			respond.JSON(w, discardLogger(), http.StatusOK, map[string]bool{"ok": true})
			return nil
		})

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ok", http.NoBody))

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if got := strings.TrimSpace(rec.Body.String()); got != `{"ok":true}` {
		t.Errorf("body = %s", got)
	}
}
