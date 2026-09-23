//go:build integration

// These tests need a real Postgres: `make up && make test-integration` locally,
// and the integration job in CI. They sit behind a build tag so `make verify`
// stays hermetic, and they fail rather than skip without POSTGRES_URL — a
// skipped integration suite reports green having tested nothing, which is the
// one outcome worse than a failure.
package postgres_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jasonnorch/go-service-starter/internal/config"
	"github.com/jasonnorch/go-service-starter/internal/postgres"
)

// isolatedURL returns POSTGRES_URL pointed at a fresh schema that is dropped
// when the test ends. Every table a test creates, including goose's version
// table, lands in that schema, so tests cannot see each other or leftovers
// from a previous run.
func isolatedURL(t *testing.T) string {
	t.Helper()

	base := os.Getenv("POSTGRES_URL")
	if base == "" {
		t.Fatal("POSTGRES_URL is not set; run `make up` and then `make test-integration`")
	}

	suffix := make([]byte, 6)
	_, _ = rand.Read(suffix)
	schema := "test_" + hex.EncodeToString(suffix)

	ctx := context.Background()
	conn, err := pgx.Connect(ctx, base)
	if err != nil {
		t.Fatalf("connecting to POSTGRES_URL: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()
	if _, err := conn.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatalf("creating schema: %v", err)
	}
	t.Cleanup(func() {
		conn, err := pgx.Connect(context.Background(), base)
		if err != nil {
			t.Errorf("connecting to drop %s: %v", schema, err)
			return
		}
		defer func() { _ = conn.Close(context.Background()) }()
		if _, err := conn.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Errorf("dropping %s: %v", schema, err)
		}
	})

	u, err := url.Parse(base)
	if err != nil {
		t.Fatalf("POSTGRES_URL is not a URL: %v", err)
	}
	q := u.Query()
	q.Set("search_path", schema) // pgx passes unknown parameters to the server
	u.RawQuery = q.Encode()
	return u.String()
}

func cfgFor(u string) config.Postgres {
	return config.Postgres{
		URL:            config.Secret(u),
		MaxConns:       4,
		ConnectTimeout: 10 * time.Second,
		MigrateOnStart: true,
	}
}

var widgets = fstest.MapFS{
	"00001_widgets.sql": {Data: []byte(`-- +goose Up
CREATE TABLE widgets (id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY);

-- +goose Down
DROP TABLE widgets;
`)},
}

// A fresh fork has an empty migrations directory. Open has to treat that as
// nothing to do, not as a failure, or the service cannot start until someone
// writes a migration it does not need.
func TestOpenStartsWithNoMigrations(t *testing.T) {
	pool, err := postgres.Open(context.Background(), cfgFor(isolatedURL(t)), discard())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer pool.Close()

	if err := pool.Ping(context.Background()); err != nil {
		t.Errorf("Ping after Open: %v", err)
	}
}

// Every restart runs Migrate. If a second run tried to reapply what the first
// did, the service would fail to start the second time it booted.
func TestMigrateIsIdempotent(t *testing.T) {
	ctx := context.Background()
	pool, err := postgres.Open(ctx, cfgFor(isolatedURL(t)), discard())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer pool.Close()

	for run := range 2 {
		if err := postgres.Migrate(ctx, pool, widgets, discard()); err != nil {
			t.Fatalf("Migrate run %d: %v", run+1, err)
		}
	}
	if _, err := pool.Exec(ctx, "INSERT INTO widgets DEFAULT VALUES"); err != nil {
		t.Errorf("the migrated table is not usable: %v", err)
	}
}

// widgetsThenGadgets is a deploy that ships one new migration on top of
// widgets. The new one holds its transaction open long enough that instances
// released together are certain to overlap, rather than finishing one after
// another by luck of scheduling.
var widgetsThenGadgets = fstest.MapFS{
	"00001_widgets.sql": widgets["00001_widgets.sql"],
	"00002_gadgets.sql": {Data: []byte(`-- +goose Up
SELECT pg_sleep(0.3);
CREATE TABLE gadgets (id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY);

-- +goose Down
DROP TABLE gadgets;
`)},
}

// Instances of a deployment start together, and each one migrates. Without
// the advisory lock, they all see the new migration as pending, all apply it,
// and every one but the first fails its startup on a duplicate relation.
//
// The database is migrated once beforehand, because that is the state a real
// rollout meets. On a fresh database the instances instead contend on goose
// creating its own version table, which goose retries its way out of even
// without the lock, so a test starting there passes with the lock removed and
// proves nothing.
func TestMigrateSerializesConcurrentInstances(t *testing.T) {
	ctx := context.Background()
	u := isolatedURL(t)

	// Pools are opened first and the migrations released together: opening a
	// pool takes long enough that instances started one by one never overlap.
	// Two is enough to race, and each extra instance adds a full poll of
	// goose's lock (5s) to the test's runtime.
	const instances = 2
	cfg := cfgFor(u)
	cfg.MigrateOnStart = false
	pools := make([]*pgxpool.Pool, instances)
	for i := range pools {
		pool, err := postgres.Open(ctx, cfg, discard())
		if err != nil {
			t.Fatalf("Open %d: %v", i, err)
		}
		t.Cleanup(pool.Close)
		pools[i] = pool
	}
	if err := postgres.Migrate(ctx, pools[0], widgets, discard()); err != nil {
		t.Fatalf("migrating the previous release: %v", err)
	}

	start := make(chan struct{})
	errs := make([]error, instances)
	var wg sync.WaitGroup
	for i, pool := range pools {
		wg.Go(func() {
			<-start
			errs[i] = postgres.Migrate(ctx, pool, widgetsThenGadgets, discard())
		})
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("instance %d: %v", i, err)
		}
	}
}

// The failure people actually hit at startup is a wrong password, and the
// error for it goes straight to the log.
func TestOpenDoesNotLeakThePasswordOnAuthFailure(t *testing.T) {
	u, err := url.Parse(isolatedURL(t))
	if err != nil {
		t.Fatal(err)
	}
	const password = "wrong-password-do-not-log"
	u.User = url.UserPassword(u.User.Username(), password)

	_, err = postgres.Open(context.Background(), cfgFor(u.String()), discard())
	if err == nil {
		t.Fatal("Open succeeded with the wrong password")
	}
	if strings.Contains(err.Error(), password) {
		t.Errorf("the password reached the error: %v", err)
	}
}
