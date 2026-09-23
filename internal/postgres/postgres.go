// Package postgres opens the service's connection pool and applies its schema
// migrations. It is the only package that imports goose, and the only one that
// configures pgx: swapping the migration tool is a change here alone. See
// docs/decisions.md, "Postgres via pgx and goose".
//
// pgx itself is not confined, on purpose. There is no repository interface
// over the pool: handlers and stores (auth/pgstore, for one) take
// *pgxpool.Pool and write SQL, because a generic database abstraction is the
// thing every caller pays for and almost no service ever swaps. Replacing the
// driver means rewriting this package and every query.
package postgres

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"

	"github.com/jasonnorch/go-service-starter/internal/config"
)

// migrations holds the schema, applied in version order by Migrate. The
// directory's README is embedded too, which is what lets the directory exist
// before the first migration does; goose reads only the .sql files.
//
//go:embed migrations
var migrations embed.FS

// Open connects a pool, confirms the database is reachable, and applies
// pending migrations when cfg.MigrateOnStart is set.
//
// It fails rather than returning a pool that has not yet reached the
// database: a service that starts without one reports ready, takes traffic,
// and fails every request that needs it. ConnectTimeout bounds only that first
// contact; migrations run under ctx, since a long migration is not a hung one.
//
// The caller owns the pool and must Close it after the server has drained —
// see app.Run for why that is a defer rather than a cancel.
func Open(ctx context.Context, cfg config.Postgres, logger *slog.Logger) (*pgxpool.Pool, error) {
	// pgx redacts the password from its own parse errors, so wrapping them
	// cannot leak it; postgres_test.go holds it to that.
	poolCfg, err := pgxpool.ParseConfig(cfg.URL.Reveal())
	if err != nil {
		return nil, fmt.Errorf("parsing postgres.url: %w", err)
	}
	poolCfg.MaxConns = cfg.MaxConns

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("creating pool: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, cfg.ConnectTimeout)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("reaching postgres within %s: %w", cfg.ConnectTimeout, err)
	}

	if cfg.MigrateOnStart {
		sub, err := fs.Sub(migrations, "migrations")
		if err != nil {
			pool.Close()
			return nil, fmt.Errorf("reading embedded migrations: %w", err)
		}
		if err := Migrate(ctx, pool, sub, logger); err != nil {
			pool.Close()
			return nil, err
		}
	}

	return pool, nil
}

// Migrate applies every pending migration in fsys, in version order, holding
// a Postgres advisory lock so that instances starting together do not race.
// It is exported for tests, which migrate a throwaway filesystem into their
// own schema; the service itself calls it only through Open.
func Migrate(ctx context.Context, pool *pgxpool.Pool, fsys fs.FS, logger *slog.Logger) error {
	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		return fmt.Errorf("creating migration lock: %w", err)
	}

	// goose speaks database/sql. This *sql.DB borrows the pool's connections,
	// and closing it leaves the pool open.
	db := stdlib.OpenDBFromPool(pool)
	defer func() { _ = db.Close() }()

	provider, err := goose.NewProvider(goose.DialectPostgres, db, fsys,
		goose.WithSessionLocker(locker),
		// No package-level migration registry: every migration this service
		// runs is in fsys, where a reader can see it.
		goose.WithDisableGlobalRegistry(true),
		goose.WithSlog(logger),
	)
	if errors.Is(err, goose.ErrNoMigrations) {
		// A fresh fork has no schema yet. That is a state, not a failure.
		return nil
	}
	if err != nil {
		return fmt.Errorf("preparing migrations: %w", err)
	}

	results, err := provider.Up(ctx)
	if err != nil {
		return fmt.Errorf("applying migrations: %w", err)
	}
	if len(results) > 0 {
		logger.InfoContext(ctx, "applied migrations", "count", len(results))
	}
	return nil
}
