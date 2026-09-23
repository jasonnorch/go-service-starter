package config

import (
	"errors"
	"fmt"
	"slices"
	"time"
)

// Validate checks the loaded configuration and reports every problem at once.
//
// Reporting all of them matters more than it looks: a validator that returns on
// the first error turns a misconfigured deployment into a sequence of restarts,
// each revealing one more mistake. The joined error is one restart.
//
// Each message names the configuration key rather than the Go field, because
// the key is what the operator can act on — it is what appears in config.yaml
// and, upper-cased with underscores, what they would set in the environment.
func (c *Config) Validate() error {
	var errs []error

	if c.Service.Name == "" {
		errs = append(errs, errors.New("service.name: must not be empty"))
	}
	if !slices.Contains([]string{EnvDevelopment, EnvProduction}, c.Service.Environment) {
		errs = append(errs, fmt.Errorf("service.environment: must be %q or %q, got %q",
			EnvDevelopment, EnvProduction, c.Service.Environment))
	}

	if c.Server.Port < 1 || c.Server.Port > 65535 {
		errs = append(errs, fmt.Errorf("server.port: must be between 1 and 65535, got %d", c.Server.Port))
	}
	errs = append(errs,
		positiveDuration("server.read_timeout", c.Server.ReadTimeout),
		positiveDuration("server.write_timeout", c.Server.WriteTimeout),
		positiveDuration("server.idle_timeout", c.Server.IdleTimeout),
		positiveDuration("server.shutdown_timeout", c.Server.ShutdownTimeout),
	)
	if c.Server.BodyLimit <= 0 {
		errs = append(errs, fmt.Errorf("server.body_limit: must be positive, got %d", c.Server.BodyLimit))
	}

	if !slices.Contains([]string{"debug", "info", "warn", "error"}, c.Log.Level) {
		errs = append(errs, fmt.Errorf("log.level: must be one of debug, info, warn, error, got %q", c.Log.Level))
	}
	if !slices.Contains([]string{FormatText, FormatJSON}, c.Log.Format) {
		errs = append(errs, fmt.Errorf("log.format: must be %q or %q, got %q",
			FormatText, FormatJSON, c.Log.Format))
	}

	// module:postgres begin
	switch {
	case !c.Postgres.URL.IsSet():
		errs = append(errs, errors.New("postgres.url: must not be empty"))
	case c.Service.Production() && c.Postgres.URL.Reveal() == DevPostgresURL:
		// Comparing the whole string, not the host: a production database on
		// localhost is unusual but legitimate, and only the literal default is
		// certain to be a mistake.
		errs = append(errs, errors.New("postgres.url: production is still using the development default; set POSTGRES_URL"))
	}
	if c.Postgres.MaxConns < 1 {
		errs = append(errs, fmt.Errorf("postgres.max_conns: must be at least 1, got %d", c.Postgres.MaxConns))
	}
	errs = append(errs, positiveDuration("postgres.connect_timeout", c.Postgres.ConnectTimeout))
	// module:postgres end

	// module:cache begin
	// The backend names are spelled out rather than taken from the cache
	// package, which imports this one.
	switch c.Cache.Backend {
	case "memory":
	case "redis":
		switch {
		case !c.Cache.RedisURL.IsSet():
			errs = append(errs, errors.New("cache.redis_url: must not be empty when cache.backend is redis"))
		case c.Service.Production() && c.Cache.RedisURL.Reveal() == DevRedisURL:
			errs = append(errs, errors.New("cache.redis_url: production is still using the development default; set CACHE_REDIS_URL"))
		}
	default:
		errs = append(errs, fmt.Errorf("cache.backend: must be %q or %q, got %q", "redis", "memory", c.Cache.Backend))
	}
	errs = append(errs, positiveDuration("cache.connect_timeout", c.Cache.ConnectTimeout))
	// module:cache end

	// errors.Join drops nils, so the unconditional appends above cost nothing
	// and it returns nil when every check passed.
	return errors.Join(errs...)
}

// positiveDuration returns an error naming key when d is not positive, and nil
// otherwise. A zero timeout is never the intent — on an http.Server it means
// "no timeout", which is how a slow client holds a connection forever — so it
// is rejected rather than silently accepted.
func positiveDuration(key string, d time.Duration) error {
	if d <= 0 {
		return fmt.Errorf("%s: must be a positive duration, got %s", key, d)
	}
	return nil
}
