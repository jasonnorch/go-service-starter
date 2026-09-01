package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonnorch/go-service-starter/internal/config"
)

// loadIn runs Load with the working directory set to an empty temp dir, so a
// config.yaml sitting in the repo root cannot reach the test. That is the exact
// failure this package exists to prevent, and a test that reads the developer's
// machine would reintroduce it here first.
func loadIn(t *testing.T, dir string) (*config.Config, error) {
	t.Helper()
	t.Chdir(dir)
	return config.Load()
}

func writeConfig(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
	return path
}

// The embedded defaults have to be complete and valid on their own: no file, no
// environment, no network.
func TestLoadDefaultsAreCompleteAndValid(t *testing.T) {
	cfg, err := loadIn(t, t.TempDir())
	if err != nil {
		t.Fatalf("Load with no file and no env: %v", err)
	}

	if cfg.Service.Name == "" {
		t.Error("service.name is empty")
	}
	if cfg.Server.Port != 8080 {
		t.Errorf("server.port = %d, want 8080", cfg.Server.Port)
	}
	if cfg.Server.ReadTimeout != 10*time.Second {
		t.Errorf("server.read_timeout = %s, want 10s", cfg.Server.ReadTimeout)
	}
	if cfg.Server.BodyLimit != 1<<20 {
		t.Errorf("server.body_limit = %d, want %d", cfg.Server.BodyLimit, 1<<20)
	}
	if cfg.Log.Format != config.FormatText {
		t.Errorf("log.format = %q, want %q", cfg.Log.Format, config.FormatText)
	}
}

// The one mechanic most likely to break silently: AutomaticEnv only reaches
// keys Viper already knows about, and the only thing registering them is the
// embedded defaults being read. If that read is ever removed or reordered, the
// service keeps starting and quietly ignores every environment variable.
func TestLoadEnvOverridesDefault(t *testing.T) {
	t.Setenv("SERVER_PORT", "9000")
	t.Setenv("SERVER_READ_TIMEOUT", "42s")
	t.Setenv("LOG_FORMAT", "json")
	t.Setenv("LOG_ADD_SOURCE", "true")

	cfg, err := loadIn(t, t.TempDir())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Server.Port != 9000 {
		t.Errorf("server.port = %d, want 9000", cfg.Server.Port)
	}
	if cfg.Server.ReadTimeout != 42*time.Second {
		t.Errorf("server.read_timeout = %s, want 42s", cfg.Server.ReadTimeout)
	}
	if cfg.Log.Format != config.FormatJSON {
		t.Errorf("log.format = %q, want json", cfg.Log.Format)
	}
	if !cfg.Log.AddSource {
		t.Error("log.add_source = false, want true")
	}
}

func TestLoadLayerPrecedence(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, config.DefaultConfigFile, "server:\n  port: 7000\n  write_timeout: 90s\n")

	t.Run("file overrides defaults", func(t *testing.T) {
		cfg, err := loadIn(t, dir)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if cfg.Server.Port != 7000 {
			t.Errorf("server.port = %d, want 7000 from file", cfg.Server.Port)
		}
		if cfg.Server.WriteTimeout != 90*time.Second {
			t.Errorf("server.write_timeout = %s, want 90s from file", cfg.Server.WriteTimeout)
		}
		// Keys the file does not mention keep their defaults: the override file
		// is a patch, not a replacement.
		if cfg.Server.ReadTimeout != 10*time.Second {
			t.Errorf("server.read_timeout = %s, want the 10s default", cfg.Server.ReadTimeout)
		}
	})

	t.Run("env overrides file", func(t *testing.T) {
		t.Setenv("SERVER_PORT", "7777")
		cfg, err := loadIn(t, dir)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if cfg.Server.Port != 7777 {
			t.Errorf("server.port = %d, want 7777 from env", cfg.Server.Port)
		}
	})
}

func TestLoadConfigFileEnv(t *testing.T) {
	t.Run("named file is read", func(t *testing.T) {
		dir := t.TempDir()
		path := writeConfig(t, dir, "elsewhere.yaml", "service:\n  name: renamed\n")
		t.Setenv(config.ConfigFileEnv, path)

		cfg, err := loadIn(t, t.TempDir())
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if cfg.Service.Name != "renamed" {
			t.Errorf("service.name = %q, want %q", cfg.Service.Name, "renamed")
		}
	})

	// Naming a file explicitly and having it silently ignored is how a
	// deployment runs on defaults while everyone believes it is configured.
	t.Run("named file that is missing is an error", func(t *testing.T) {
		t.Setenv(config.ConfigFileEnv, filepath.Join(t.TempDir(), "absent.yaml"))

		if _, err := loadIn(t, t.TempDir()); err == nil {
			t.Fatal("Load succeeded with CONFIG_FILE naming a missing file, want an error")
		}
	})

	// Whereas the default file is genuinely optional.
	t.Run("absent default file is not an error", func(t *testing.T) {
		if _, err := loadIn(t, t.TempDir()); err != nil {
			t.Fatalf("Load with no %s: %v", config.DefaultConfigFile, err)
		}
	})
}

func TestLoadRejectsInvalidConfig(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, config.DefaultConfigFile, "server:\n  port: 70000\n")

	_, err := loadIn(t, dir)
	if err == nil {
		t.Fatal("Load succeeded with an out-of-range port, want an error")
	}
	if !strings.Contains(err.Error(), "server.port") {
		t.Errorf("error does not name the offending key: %v", err)
	}
}

func TestLoadMalformedFile(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, config.DefaultConfigFile, "server:\n\tport: [unclosed\n")

	if _, err := loadIn(t, dir); err == nil {
		t.Fatal("Load succeeded on malformed YAML, want an error")
	}
}
