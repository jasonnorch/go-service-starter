package auth_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jasonnorch/go-service-starter/internal/auth"
)

func discard() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// configStore returns a ConfigStore allowing one freshly generated key.
func configStore(t *testing.T, name string) (store *auth.ConfigStore, key string) {
	t.Helper()
	key, err := auth.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	hash := auth.HashKey(key)
	store, err = auth.NewConfigStore(name + "=" + hex.EncodeToString(hash[:]))
	if err != nil {
		t.Fatal(err)
	}
	return store, key
}

// serve runs a request with the given Authorization header through Require,
// and reports what the protected handler saw.
func serve(store auth.KeyStore, authorization string) (*httptest.ResponseRecorder, *auth.Principal) {
	var seen *auth.Principal
	h := auth.Require(store, discard())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if p, ok := auth.FromContext(r.Context()); ok {
			seen = &p
		}
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/v1/whoami", http.NoBody)
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec, seen
}

func TestRequireLetsAValidKeyThrough(t *testing.T) {
	store, key := configStore(t, "ci")

	for _, scheme := range []string{"Bearer", "bearer", "BEARER"} {
		rec, seen := serve(store, scheme+" "+key)
		if rec.Code != http.StatusOK {
			t.Errorf("%s: status = %d, want 200", scheme, rec.Code)
		}
		if seen == nil || seen.Name != "ci" || seen.ID != "config:ci" {
			t.Errorf("%s: handler saw principal %+v, want ci", scheme, seen)
		}
	}
}

// Every way a key can be wrong gets the same response. If a missing header,
// an unknown key and a malformed one answered differently, a caller probing
// keys would learn which of its guesses were closer.
func TestRequireRejectsEveryBadKeyTheSameWay(t *testing.T) {
	store, key := configStore(t, "ci")
	cases := []struct {
		name          string
		authorization string
	}{
		{"no header", ""},
		{"wrong scheme", "Basic " + key},
		{"no key", "Bearer "},
		{"unknown key", "Bearer sk_notarealkey"},
		{"key without a scheme", key},
		{"key with a trailing character", "Bearer " + key + "x"},
	}

	var first string
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			rec, seen := serve(store, tt.authorization)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", rec.Code)
			}
			if seen != nil {
				t.Error("the protected handler ran")
			}
			if got := rec.Header().Get("WWW-Authenticate"); !strings.HasPrefix(got, "Bearer") {
				t.Errorf("WWW-Authenticate = %q; a 401 must say how to authenticate", got)
			}
			var body struct {
				Error struct{ Code, Message string } `json:"error"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("body is not the error envelope: %s", rec.Body.String())
			}
			got := body.Error.Code + "|" + body.Error.Message
			if first == "" {
				first = got
			} else if got != first {
				t.Errorf("response %q differs from %q; bad keys must be indistinguishable", got, first)
			}
		})
	}
}

type failingStore struct{}

func (failingStore) Lookup(context.Context, [sha256.Size]byte) (auth.Principal, bool, error) {
	return auth.Principal{}, false, errors.New("connection refused to db.internal:5432")
}

// A store that cannot answer is a server fault, not the caller's: a 401 would
// tell a legitimate client its key is bad, and the cause must not reach it.
func TestRequireTurnsAStoreFailureIntoA500(t *testing.T) {
	rec, seen := serve(failingStore{}, "Bearer sk_anything")
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
	if seen != nil {
		t.Error("the protected handler ran")
	}
	if strings.Contains(rec.Body.String(), "db.internal") {
		t.Errorf("the store's error reached the client: %s", rec.Body.String())
	}
}

// Config comes first, so a store error after a config match does not matter,
// but a store error before any match fails closed rather than falling through.
func TestStoresTriesInOrderAndFailsClosed(t *testing.T) {
	store, key := configStore(t, "bootstrap")

	if _, ok, err := (auth.Stores{store, failingStore{}}).Lookup(context.Background(), auth.HashKey(key)); !ok || err != nil {
		t.Errorf("a config key was not accepted ahead of a failing store: ok=%v err=%v", ok, err)
	}
	if _, ok, err := (auth.Stores{store, failingStore{}}).Lookup(context.Background(), auth.HashKey("sk_other")); ok || err == nil {
		t.Errorf("an unknown key fell past a failing store: ok=%v err=%v", ok, err)
	}
}

func TestNewConfigStore(t *testing.T) {
	valid := strings.Repeat("ab", sha256.Size)
	cases := []struct {
		name string
		spec string
		ok   bool
	}{
		{"empty", "", true},
		{"one", "ci=" + valid, true},
		{"several, with spaces", " ci=" + valid + " , admin = " + valid + " ", true},
		{"trailing comma", "ci=" + valid + ",", true},
		{"no equals", "ci" + valid, false},
		{"no name", "=" + valid, false},
		{"short hash", "ci=abcd", false},
		{"not hex", "ci=" + strings.Repeat("zz", sha256.Size), false},
		// The most likely mistake: pasting the key where its hash belongs.
		{"a raw key", "ci=sk_Zm9vYmFyYmF6cXV4cXV1eGZvb2Jhcg", false},
		{"duplicate name", "ci=" + valid + ",ci=" + valid, false},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			_, err := auth.NewConfigStore(tt.spec)
			if tt.ok && err != nil {
				t.Errorf("rejected a valid spec: %v", err)
			}
			if !tt.ok {
				if !errors.Is(err, auth.ErrMalformedKeys) {
					t.Errorf("err = %v, want ErrMalformedKeys", err)
				}
				// The error goes to the startup log; a pasted key must not.
				if err != nil && strings.Contains(err.Error(), "sk_") {
					t.Errorf("the error echoes the entry: %v", err)
				}
			}
		})
	}
}

func TestNewKeyIsRandomAndRecognizable(t *testing.T) {
	a, _ := auth.NewKey()
	b, _ := auth.NewKey()
	if a == b {
		t.Error("two keys were identical")
	}
	if !strings.HasPrefix(a, "sk_") || len(a) < 40 {
		t.Errorf("key %q lacks the sk_ prefix or 32 bytes of entropy", a)
	}
}
