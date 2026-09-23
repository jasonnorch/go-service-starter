// module:postgres,auth begin
// The marker precedes the build constraint so that removal empties the whole
// file; left below it, removal would leave a file holding only the constraint.

//go:build integration

// Against a real Postgres, with the service's own migrations applied in a
// throwaway schema. Fails rather than skips without POSTGRES_URL, like the
// other integration tests.
package pgstore_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"log/slog"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jasonnorch/go-service-starter/internal/auth"
	"github.com/jasonnorch/go-service-starter/internal/auth/pgstore"
	"github.com/jasonnorch/go-service-starter/internal/config"
	"github.com/jasonnorch/go-service-starter/internal/postgres"
)

// openMigrated opens a pool on a fresh schema, dropped when the test ends,
// with every embedded migration applied — so the table under test is the one
// production gets, not a copy of its DDL.
func openMigrated(t *testing.T) *pgxpool.Pool {
	t.Helper()
	base := os.Getenv("POSTGRES_URL")
	if base == "" {
		t.Fatal("POSTGRES_URL is not set; run `make up` and then `make test-integration`")
	}

	b := make([]byte, 6)
	_, _ = rand.Read(b)
	schema := "test_" + hex.EncodeToString(b)

	ctx := context.Background()
	conn, err := pgx.Connect(ctx, base)
	if err != nil {
		t.Fatalf("connecting: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()
	if _, err := conn.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatalf("creating schema: %v", err)
	}
	t.Cleanup(func() {
		c, err := pgx.Connect(context.Background(), base)
		if err != nil {
			t.Errorf("connecting to drop %s: %v", schema, err)
			return
		}
		defer func() { _ = c.Close(context.Background()) }()
		if _, err := c.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Errorf("dropping %s: %v", schema, err)
		}
	})

	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()

	pool, err := postgres.Open(ctx, config.Postgres{
		URL:            config.Secret(u.String()),
		MaxConns:       2,
		ConnectTimeout: 10 * time.Second,
		MigrateOnStart: true,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// The two behaviors a database key store adds over config: keys issued at
// runtime work, and revoking one takes effect without a deploy.
func TestLookupFindsActiveKeysAndNotRevokedOnes(t *testing.T) {
	ctx := context.Background()
	pool := openMigrated(t)
	store := pgstore.New(pool)

	key, _ := auth.NewKey()
	hash := auth.HashKey(key)
	var id int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO api_keys (name, key_hash) VALUES ($1, $2) RETURNING id`, "deploy", hash[:],
	).Scan(&id); err != nil {
		t.Fatalf("inserting a key: %v", err)
	}

	p, ok, err := store.Lookup(ctx, hash)
	if err != nil || !ok || p.Name != "deploy" {
		t.Fatalf("Lookup(active) = %+v, %v, %v; want deploy", p, ok, err)
	}

	if _, err := pool.Exec(ctx, `UPDATE api_keys SET revoked_at = now() WHERE id = $1`, id); err != nil {
		t.Fatalf("revoking: %v", err)
	}
	if _, ok, err := store.Lookup(ctx, hash); ok || err != nil {
		t.Errorf("Lookup(revoked) = %v, %v; a revoked key must not authenticate", ok, err)
	}

	if _, ok, err := store.Lookup(ctx, auth.HashKey("sk_never-issued")); ok || err != nil {
		t.Errorf("Lookup(unknown) = %v, %v; want false, nil", ok, err)
	}
}

// module:postgres,auth end
