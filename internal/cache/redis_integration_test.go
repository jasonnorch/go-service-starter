//go:build integration

// Runs the same contract as Memory against a real Redis: `make up && make
// test-integration` locally, the integration job in CI. Like the Postgres
// tests, it fails rather than skips without CACHE_REDIS_URL, so a missing
// server is a red run and not a green one that tested nothing.
package cache_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jasonnorch/go-service-starter/internal/cache"
	"github.com/jasonnorch/go-service-starter/internal/config"
)

func TestRedisContract(t *testing.T) {
	url := os.Getenv("CACHE_REDIS_URL")
	if url == "" {
		t.Fatal("CACHE_REDIS_URL is not set; run `make up` and then `make test-integration`")
	}

	runContract(t, func(t *testing.T) cache.Cache {
		c, err := cache.Open(context.Background(), config.Cache{
			Backend:        cache.BackendRedis,
			RedisURL:       config.Secret(url),
			ConnectTimeout: 5 * time.Second,
		})
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		t.Cleanup(func() { _ = c.Close() })
		return c
	})
}
