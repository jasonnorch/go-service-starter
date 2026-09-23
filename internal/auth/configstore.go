package auth

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"strings"
)

// ConfigStore is the keys listed in auth.keys: "name=sha256hex", comma
// separated. One string rather than a YAML list, so the whole set fits in a
// single AUTH_KEYS environment variable, which is where production keys live.
//
// It is always consulted first, so a key here works even when the database
// is empty or down — the bootstrap key that creates the rest, or a break-glass
// key for an operator.
type ConfigStore struct {
	keys []configKey
}

type configKey struct {
	name string
	hash [sha256.Size]byte
}

// NewConfigStore parses spec. An empty spec is valid and matches nothing.
// Errors name the entry's position but never echo it, because a mistake here
// is often a raw key pasted where its hash belongs.
func NewConfigStore(spec string) (*ConfigStore, error) {
	s := &ConfigStore{}
	seen := map[string]bool{}
	for i, entry := range strings.Split(spec, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		name, digest, ok := strings.Cut(entry, "=")
		name = strings.TrimSpace(name)
		if !ok || name == "" {
			return nil, fmt.Errorf("%w: entry %d is not name=sha256hex", ErrMalformedKeys, i+1)
		}
		raw, err := hex.DecodeString(strings.TrimSpace(digest))
		if err != nil || len(raw) != sha256.Size {
			return nil, fmt.Errorf("%w: entry %d (%s) is not a 64-character hex SHA-256; store the key's hash, never the key",
				ErrMalformedKeys, i+1, name)
		}
		if seen[name] {
			return nil, fmt.Errorf("%w: name %q appears twice", ErrMalformedKeys, name)
		}
		seen[name] = true
		s.keys = append(s.keys, configKey{name: name, hash: [sha256.Size]byte(raw)})
	}
	return s, nil
}

// Lookup implements KeyStore. It compares against every key in constant time
// and never returns early, so the response time says nothing about which
// entry, if any, came close.
func (s *ConfigStore) Lookup(_ context.Context, hash [sha256.Size]byte) (Principal, bool, error) {
	var found Principal
	matched := 0
	for _, k := range s.keys {
		if subtle.ConstantTimeCompare(k.hash[:], hash[:]) == 1 {
			found = Principal{ID: "config:" + k.name, Name: k.name}
			matched = 1
		}
	}
	return found, matched == 1, nil
}
