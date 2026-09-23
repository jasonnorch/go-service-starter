package config_test

import (
	"strings"
	"testing"
	"time"

	"github.com/jasonnorch/go-service-starter/internal/config"
)

func valid() *config.Config {
	return &config.Config{
		Service: config.Service{Name: "svc", Environment: config.EnvDevelopment},
		Server: config.Server{
			Port:            8080,
			ReadTimeout:     10 * time.Second,
			WriteTimeout:    30 * time.Second,
			IdleTimeout:     60 * time.Second,
			ShutdownTimeout: 15 * time.Second,
			BodyLimit:       1 << 20,
		},
		Log: config.Log{Level: "info", Format: config.FormatText},
		// module:postgres begin
		Postgres: config.Postgres{
			URL:            config.DevPostgresURL,
			MaxConns:       10,
			ConnectTimeout: 10 * time.Second,
		},
		// module:postgres end
		// module:cache begin
		Cache: config.Cache{
			Backend:        "redis",
			RedisURL:       config.DevRedisURL,
			ConnectTimeout: 5 * time.Second,
		},
		// module:cache end
	}
}

func TestValidateAcceptsValidConfig(t *testing.T) {
	if err := valid().Validate(); err != nil {
		t.Fatalf("Validate on a valid config: %v", err)
	}
}

// module:cache begin
// The memory backend needs no URL, so an empty one must not fail validation;
// otherwise a test or single-instance setup has to invent a Redis URL it never
// uses.
func TestValidateMemoryCacheNeedsNoURL(t *testing.T) {
	cfg := valid()
	cfg.Cache.Backend = "memory"
	cfg.Cache.RedisURL = ""
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate rejected a memory cache with no URL: %v", err)
	}
}

// module:cache end

func TestValidateNamesTheOffendingKey(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*config.Config)
		wantKey string
	}{
		{"empty name", func(c *config.Config) { c.Service.Name = "" }, "service.name"},
		{"unknown environment", func(c *config.Config) { c.Service.Environment = "staging" }, "service.environment"},
		{"port too high", func(c *config.Config) { c.Server.Port = 70000 }, "server.port"},
		{"port zero", func(c *config.Config) { c.Server.Port = 0 }, "server.port"},
		{"zero read timeout", func(c *config.Config) { c.Server.ReadTimeout = 0 }, "server.read_timeout"},
		{"negative write timeout", func(c *config.Config) { c.Server.WriteTimeout = -time.Second }, "server.write_timeout"},
		{"zero idle timeout", func(c *config.Config) { c.Server.IdleTimeout = 0 }, "server.idle_timeout"},
		{"zero shutdown timeout", func(c *config.Config) { c.Server.ShutdownTimeout = 0 }, "server.shutdown_timeout"},
		{"zero body limit", func(c *config.Config) { c.Server.BodyLimit = 0 }, "server.body_limit"},
		{"unknown level", func(c *config.Config) { c.Log.Level = "verbose" }, "log.level"},
		{"unknown format", func(c *config.Config) { c.Log.Format = "logfmt" }, "log.format"},
		// module:postgres begin
		{"empty postgres url", func(c *config.Config) { c.Postgres.URL = "" }, "postgres.url"},
		// The failure this prevents: a production deploy missing POSTGRES_URL
		// starts, dials localhost, and fails in a way that looks like an outage
		// rather than a configuration error.
		{"development postgres url in production", func(c *config.Config) {
			c.Service.Environment = config.EnvProduction
		}, "postgres.url"},
		{"zero max conns", func(c *config.Config) { c.Postgres.MaxConns = 0 }, "postgres.max_conns"},
		{"zero connect timeout", func(c *config.Config) { c.Postgres.ConnectTimeout = 0 }, "postgres.connect_timeout"},
		// module:postgres end
		// module:cache begin
		{"unknown cache backend", func(c *config.Config) { c.Cache.Backend = "memcached" }, "cache.backend"},
		{"redis backend without a url", func(c *config.Config) { c.Cache.RedisURL = "" }, "cache.redis_url"},
		// A production deploy missing CACHE_REDIS_URL would otherwise start and
		// dial a Redis on localhost that is not there.
		{"development redis url in production", func(c *config.Config) {
			c.Service.Environment = config.EnvProduction
			c.Postgres.URL = "postgres://prod" // module:postgres
		}, "cache.redis_url"},
		{"zero cache connect timeout", func(c *config.Config) { c.Cache.ConnectTimeout = 0 }, "cache.connect_timeout"},
		// module:cache end
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := valid()
			tt.mutate(cfg)

			err := cfg.Validate()
			if err == nil {
				t.Fatal("Validate accepted an invalid config")
			}
			if !strings.Contains(err.Error(), tt.wantKey) {
				t.Errorf("error does not name %s: %v", tt.wantKey, err)
			}
		})
	}
}

// One restart should reveal every mistake, not the first one.
func TestValidateReportsEveryProblemAtOnce(t *testing.T) {
	cfg := valid()
	cfg.Service.Name = ""
	cfg.Server.Port = -1
	cfg.Server.ReadTimeout = 0
	cfg.Log.Level = "verbose"

	err := cfg.Validate()
	if err == nil {
		t.Fatal("Validate accepted an invalid config")
	}

	for _, key := range []string{"service.name", "server.port", "server.read_timeout", "log.level"} {
		if !strings.Contains(err.Error(), key) {
			t.Errorf("joined error is missing %s:\n%v", key, err)
		}
	}
}

func TestServiceProduction(t *testing.T) {
	if (config.Service{Environment: config.EnvProduction}).Production() != true {
		t.Error("Production() = false for the production environment")
	}
	if (config.Service{Environment: config.EnvDevelopment}).Production() != false {
		t.Error("Production() = true for the development environment")
	}
}
