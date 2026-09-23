// Package config loads the service's configuration from three layers —
// embedded defaults, an optional file, and the environment — and returns it as
// a typed struct.
//
// The package deliberately has no package-level state and no init(). Load
// returns a value, callers own it, and tests construct a Config literal without
// touching the filesystem. Only app.Run holds the whole *Config; every other
// package takes the one sub-struct it needs, which keeps a package's
// configuration surface visible in its constructor signature.
package config

import "time"

// Config is the whole of the service's configuration.
//
// The mapstructure and json tags are kept identical on purpose. mapstructure
// names the key in defaults.yaml and therefore the environment variable, while
// json is what an /info endpoint serves — so a single struct is both the
// loading target and the reported state, with no hand-maintained snapshot type
// to fall out of sync.
type Config struct {
	Service  Service  `mapstructure:"service"  json:"service"`
	Server   Server   `mapstructure:"server"   json:"server"`
	Log      Log      `mapstructure:"log"      json:"log"`
	Postgres Postgres `mapstructure:"postgres" json:"postgres"` // module:postgres
}

// Service holds the service's identity.
type Service struct {
	Name string `mapstructure:"name"        json:"name"`
	// Environment is "development" or "production".
	Environment string `mapstructure:"environment" json:"environment"`
}

// Production reports whether the service is configured for production. Prefer
// this over comparing the string, so the set of accepted values stays in
// Validate rather than spreading across call sites.
func (s Service) Production() bool { return s.Environment == EnvProduction }

// Recognized values for Service.Environment.
const (
	EnvDevelopment = "development"
	EnvProduction  = "production"
)

// Server holds the HTTP server's parameters. Passed whole to httpapi.NewServer.
type Server struct {
	Port            int           `mapstructure:"port"             json:"port"`
	ReadTimeout     time.Duration `mapstructure:"read_timeout"     json:"read_timeout"`
	WriteTimeout    time.Duration `mapstructure:"write_timeout"    json:"write_timeout"`
	IdleTimeout     time.Duration `mapstructure:"idle_timeout"     json:"idle_timeout"`
	ShutdownTimeout time.Duration `mapstructure:"shutdown_timeout" json:"shutdown_timeout"`
	BodyLimit       int64         `mapstructure:"body_limit"       json:"body_limit"`
}

// Log holds logging parameters. Passed whole to logging.New.
type Log struct {
	Level     string `mapstructure:"level"      json:"level"`
	Format    string `mapstructure:"format"     json:"format"`
	AddSource bool   `mapstructure:"add_source" json:"add_source"`
}

// Recognized values for Log.Format.
const (
	FormatText = "text"
	FormatJSON = "json"
)

// module:postgres begin

// Postgres holds the database pool's parameters. Passed whole to postgres.Open.
type Postgres struct {
	// URL is a libpq connection string. It is a Secret because it carries the
	// password.
	URL            Secret        `mapstructure:"url"              json:"url"`
	MaxConns       int32         `mapstructure:"max_conns"        json:"max_conns"`
	ConnectTimeout time.Duration `mapstructure:"connect_timeout"  json:"connect_timeout"`
	MigrateOnStart bool          `mapstructure:"migrate_on_start" json:"migrate_on_start"`
}

// DevPostgresURL is the default postgres.url: the database compose.yaml runs,
// so `make up && make run` works on a fresh clone. Validate rejects it in
// production, which turns a deploy that forgot POSTGRES_URL into a startup
// error rather than a service quietly dialing localhost. A test holds
// defaults.yaml to this value, so the two cannot drift.
const DevPostgresURL = "postgres://app:app@localhost:5432/app?sslmode=disable" //nolint:gosec // the local compose container's credentials, rejected in production by Validate

// module:postgres end
