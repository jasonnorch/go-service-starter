package cache_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jasonnorch/go-service-starter/internal/cache"
	"github.com/jasonnorch/go-service-starter/internal/config"
)

// Open's error goes to the startup log. If the URL's password were echoed in
// it, every failed start would write the Redis password into the logs. Each
// URL here fails to parse, so the test needs no server and dials nothing.
func TestOpenRedisDoesNotLeakThePasswordOnAParseError(t *testing.T) {
	const password = "hunter2-do-not-log"
	urls := []struct {
		name string
		url  string
	}{
		{"non-numeric port", "redis://:" + password + "@localhost:notaport/0"},
		{"non-numeric db", "redis://:" + password + "@localhost:6379/notadb"},
		{"unclosed ipv6 bracket", "redis://:" + password + "@[::1/0"},
		{"unknown scheme", "redix://:" + password + "@localhost:6379/0"},
		{"control character", "redis://:" + password + "@local\x7fhost:6379/0"},
	}

	for _, tt := range urls {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.Cache{
				Backend:        cache.BackendRedis,
				RedisURL:       config.Secret(tt.url),
				ConnectTimeout: time.Second,
			}
			c, err := cache.Open(context.Background(), cfg)
			if err == nil {
				_ = c.Close()
				t.Fatal("Open accepted a malformed URL")
			}
			if strings.Contains(err.Error(), password) {
				t.Errorf("the password reached the error: %v", err)
			}
		})
	}
}
