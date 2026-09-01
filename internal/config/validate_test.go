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
	}
}

func TestValidateAcceptsValidConfig(t *testing.T) {
	if err := valid().Validate(); err != nil {
		t.Fatalf("Validate on a valid config: %v", err)
	}
}

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
