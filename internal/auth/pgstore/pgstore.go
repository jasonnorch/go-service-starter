// module:postgres,auth begin

// Package pgstore is the API keys kept in Postgres, for services that issue
// and revoke keys at runtime. It exists only while both the postgres and auth
// modules do: `./setup.sh --without` either one deletes this file, and auth
// falls back to its ConfigStore alone.
//
// The table is created by internal/postgres/migrations/00001_api_keys.sql.
// Insert rows with the hash `go run ./cmd/apikey` prints; revoke by setting
// revoked_at.
package pgstore

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jasonnorch/go-service-starter/internal/auth"
)

// Store is an auth.KeyStore over the api_keys table.
type Store struct {
	pool *pgxpool.Pool
}

// New returns a Store that queries pool.
func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Lookup implements auth.KeyStore. A revoked key is simply not found, which
// Require answers exactly like an unknown one.
func (s *Store) Lookup(ctx context.Context, hash [sha256.Size]byte) (auth.Principal, bool, error) {
	var (
		id   int64
		name string
	)
	err := s.pool.QueryRow(ctx,
		`SELECT id, name FROM api_keys WHERE key_hash = $1 AND revoked_at IS NULL`,
		hash[:],
	).Scan(&id, &name)
	if errors.Is(err, pgx.ErrNoRows) {
		return auth.Principal{}, false, nil
	}
	if err != nil {
		return auth.Principal{}, false, fmt.Errorf("querying api_keys: %w", err)
	}
	return auth.Principal{ID: "key:" + strconv.FormatInt(id, 10), Name: name}, true, nil
}

// module:postgres,auth end
