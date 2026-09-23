// Package auth authenticates requests by API key: a middleware that reads a
// bearer key, looks up its hash in a KeyStore, and puts the caller's Principal
// on the request context.
//
// Keys are never stored, only their SHA-256. A key is 32 random bytes, so
// its hash is as good as a password hash without the cost of one: there is
// nothing to brute-force and nothing to salt.
//
// Where keys live is a KeyStore. ConfigStore reads them from configuration and
// is always present; with the postgres module, pgstore adds a table of them.
// See docs/decisions.md, "API keys, from config and the database".
//
// Everything after Require sees only Principal, so replacing API keys with JWT
// or OIDC is a new middleware that sets the same Principal — nothing
// downstream changes.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/jasonnorch/go-service-starter/internal/httpapi/respond"
)

// Principal is who an authenticated request is acting as.
type Principal struct {
	// ID is unique across stores: "config:<name>" or "key:<row id>".
	ID   string `json:"id"`
	Name string `json:"name"`
}

type principalKey struct{}

// FromContext returns the request's Principal, and false when the request did
// not pass through Require.
func FromContext(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(Principal)
	return p, ok
}

// KeyStore looks up a key by its SHA-256. A key it does not know is (zero,
// false, nil); an error means the lookup itself failed and is a 500, not a 401.
type KeyStore interface {
	Lookup(ctx context.Context, hash [sha256.Size]byte) (Principal, bool, error)
}

// Stores tries each store in order and returns the first match. An error from
// any store fails the lookup rather than falling through to the next: a
// database that cannot answer must not quietly narrow who is allowed in, nor
// make a revoked key look merely unknown.
type Stores []KeyStore

// Lookup implements KeyStore.
func (s Stores) Lookup(ctx context.Context, hash [sha256.Size]byte) (Principal, bool, error) {
	for _, store := range s {
		p, ok, err := store.Lookup(ctx, hash)
		if err != nil || ok {
			return p, ok, err
		}
	}
	return Principal{}, false, nil
}

// keyPrefix marks a string as one of this service's keys, so a leaked key is
// recognizable in a log or a secret scanner rather than just 43 random
// characters.
const keyPrefix = "sk_"

// NewKey returns a new random API key. Show it once and store only HashKey of
// it; it cannot be recovered from the hash.
func NewKey() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating key: %w", err)
	}
	return keyPrefix + base64.RawURLEncoding.EncodeToString(b), nil
}

// HashKey is what a KeyStore holds in place of the key.
func HashKey(key string) [sha256.Size]byte {
	return sha256.Sum256([]byte(key))
}

// errUnauthenticated is the one response for every way a key can be wrong —
// missing, malformed, unknown or revoked — so a caller probing keys learns
// nothing from which one it got.
var errUnauthenticated = respond.Unauthorized("a valid API key is required")

// Require rejects requests without a valid API key with 401, and passes the
// rest on with their Principal in the context. A store failure is a 500 whose
// cause is logged, never sent.
func Require(store KeyStore, logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key, ok := bearer(r)
			if !ok {
				unauthenticated(w, r, logger)
				return
			}
			p, ok, err := store.Lookup(r.Context(), HashKey(key))
			if err != nil {
				respond.Fail(w, r, logger, fmt.Errorf("looking up api key: %w", err))
				return
			}
			if !ok {
				unauthenticated(w, r, logger)
				return
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), principalKey{}, p)))
		})
	}
}

// bearer extracts the key from "Authorization: Bearer <key>". The scheme is
// matched case-insensitively, as RFC 9110 requires.
func bearer(r *http.Request) (string, bool) {
	scheme, key, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}
	key = strings.TrimSpace(key)
	return key, key != ""
}

func unauthenticated(w http.ResponseWriter, r *http.Request, logger *slog.Logger) {
	// RFC 9110: a 401 must say how to authenticate.
	w.Header().Set("WWW-Authenticate", `Bearer realm="api"`)
	respond.Fail(w, r, logger, errUnauthenticated)
}

// ErrMalformedKeys is wrapped by NewConfigStore's error for a bad auth.keys.
var ErrMalformedKeys = errors.New("malformed auth.keys")
