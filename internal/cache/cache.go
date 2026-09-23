// Package cache is a small key-value cache with two implementations: Redis,
// for anything running more than one instance, and an in-process Memory cache
// for tests and single-instance services. Open picks one from configuration.
//
// Unlike internal/postgres, this package puts an interface in front of the
// backend, and the difference is deliberate: caches genuinely are swapped
// (Redis, Memcached, a managed service, nothing at all in tests), the surface
// is three operations, and Memory already proves the seam holds. See
// docs/decisions.md, "A cache behind a small interface".
//
// This is the only package that imports go-redis.
package cache

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jasonnorch/go-service-starter/internal/config"
)

// Cache is what both implementations satisfy, and what cache_test.go's
// contract holds them to. A consumer that needs less — only Get, say — should
// declare its own narrower interface rather than take this one.
type Cache interface {
	// Get returns the value and true, or nil and false when the key is absent
	// or expired. A missing key is not an error.
	Get(ctx context.Context, key string) (value []byte, ok bool, err error)

	// Set stores value under key for ttl, which must be positive: an entry
	// that never expires is a memory leak with a delay on it.
	Set(ctx context.Context, key string, value []byte, ttl time.Duration) error

	// Delete removes key. Deleting a key that is not there is not an error.
	Delete(ctx context.Context, key string) error

	// Ping reports whether the cache is reachable, for the readiness check.
	Ping(ctx context.Context) error

	// Close releases the connection or background work. Call it after the
	// server has drained.
	Close() error
}

// ErrInvalidTTL is returned by Set for a ttl that is not positive.
var ErrInvalidTTL = errors.New("cache: ttl must be positive")

// Recognized values for config.Cache.Backend.
const (
	BackendRedis  = "redis"
	BackendMemory = "memory"
)

// Open returns the backend named by cfg.Backend. For Redis it confirms the
// server is reachable within cfg.ConnectTimeout and fails otherwise, for the
// same reason postgres.Open does: a service that starts without its cache
// reports ready and then fails, or slows, every request that uses it.
func Open(ctx context.Context, cfg config.Cache) (Cache, error) {
	switch cfg.Backend {
	case BackendMemory:
		return NewMemory(time.Minute), nil
	case BackendRedis:
		return openRedis(ctx, cfg)
	default:
		// Validate rejects this first; the case exists so a Config built in
		// code cannot slip past it.
		return nil, fmt.Errorf("unknown cache backend %q", cfg.Backend)
	}
}
