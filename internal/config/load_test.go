package config_test

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/spf13/viper"

	"github.com/jasonnorch/go-service-starter/internal/config"
)

// configEnvPrefixes are the top-level sections of defaults.yaml. Viper maps
// service.name onto SERVICE_NAME, so any variable carrying one of these
// prefixes can change what Load returns. Matching on the prefix rather than
// listing every key means a new key under an existing section is isolated
// automatically; only a new top-level section needs adding here.
var configEnvPrefixes = []string{"SERVICE_", "SERVER_", "LOG_", "POSTGRES_"}

// isolate removes every input Load reads except the embedded defaults: the
// working directory is moved somewhere with no config.yaml, and any
// configuration variable inherited from the shell or the CI runner is cleared.
//
// Call it first in a test, before setting the variables that test needs.
//
// Without the environment half, a developer with SERVER_PORT exported — or a CI
// image that sets LOG_FORMAT — gets different results from the same code. That
// is precisely the failure this package was built to avoid, and a test suite
// that reads the machine it runs on would reintroduce it here first.
//
// Clearing to empty rather than unsetting is deliberate and sufficient: Viper
// ignores an empty environment variable unless AllowEmptyEnv is set, which it
// is not, and t.Setenv restores the previous value when the test ends.
func isolate(t *testing.T, dir string) {
	t.Helper()

	t.Setenv(config.ConfigFileEnv, "")
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		for _, prefix := range configEnvPrefixes {
			if strings.HasPrefix(key, prefix) {
				t.Setenv(key, "")
				break
			}
		}
	}

	t.Chdir(dir)
}

// loadIn isolates the environment, then loads.
func loadIn(t *testing.T, dir string) (*config.Config, error) {
	t.Helper()
	isolate(t, dir)
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
	// Validate rejects DevPostgresURL in production by exact comparison, so the
	// file drifting from the constant would silently disable that check.
	if got := cfg.Postgres.URL.Reveal(); got != config.DevPostgresURL {
		t.Errorf("postgres.url default = %q, want config.DevPostgresURL", got)
	}
}

// defaults.yaml must list every key in Config, because reading it is what
// registers a key with Viper, and AutomaticEnv reaches only registered keys. A
// field added to the struct but not the file still loads its zero value and
// still starts, while its environment variable is silently ignored — nothing
// else fails, so this test is the only thing standing between that and
// production. The reverse direction catches a key in the file that matches no
// field, which is a typo that loads nothing.
func TestDefaultsListEveryKey(t *testing.T) {
	v := viper.New()
	v.SetConfigType("yaml")
	if err := v.ReadConfig(bytes.NewReader(config.DefaultsYAML)); err != nil {
		t.Fatalf("reading embedded defaults: %v", err)
	}
	inFile := map[string]bool{}
	for _, key := range v.AllKeys() {
		inFile[key] = true
	}

	inStruct := map[string]bool{}
	collectKeys(reflect.TypeFor[config.Config](), "", inStruct)

	for key := range inStruct {
		if !inFile[key] {
			t.Errorf("%s is in config.Config but not defaults.yaml; its environment variable would be ignored", key)
		}
	}
	for key := range inFile {
		if !inStruct[key] {
			t.Errorf("%s is in defaults.yaml but matches no field in config.Config", key)
		}
	}
}

// collectKeys walks a config struct and records the dotted key of every leaf,
// the way Viper names them. Nested structs are sections; everything else,
// including time.Duration and Secret, is a value.
func collectKeys(typ reflect.Type, prefix string, keys map[string]bool) {
	for field := range typ.Fields() {
		key := prefix + field.Tag.Get("mapstructure")
		if field.Type.Kind() == reflect.Struct {
			collectKeys(field.Type, key+".", keys)
			continue
		}
		keys[key] = true
	}
}

// The one mechanic most likely to break silently: AutomaticEnv only reaches
// keys Viper already knows about, and the only thing registering them is the
// embedded defaults being read. If that read is ever removed or reordered, the
// service keeps starting and quietly ignores every environment variable.
func TestLoadEnvOverridesDefault(t *testing.T) {
	// isolate first, then set: the helper clears configuration variables, so
	// setting them beforehand would wipe the test's own setup.
	isolate(t, t.TempDir())
	t.Setenv("SERVER_PORT", "9000")
	t.Setenv("SERVER_READ_TIMEOUT", "42s")
	t.Setenv("LOG_FORMAT", "json")
	t.Setenv("LOG_ADD_SOURCE", "true")

	cfg, err := config.Load()
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
		isolate(t, dir)
		t.Setenv("SERVER_PORT", "7777")

		cfg, err := config.Load()
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

		isolate(t, t.TempDir())
		t.Setenv(config.ConfigFileEnv, path)

		cfg, err := config.Load()
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
		missing := filepath.Join(t.TempDir(), "absent.yaml")

		isolate(t, t.TempDir())
		t.Setenv(config.ConfigFileEnv, missing)

		if _, err := config.Load(); err == nil {
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

// Guards the guard. A developer with SERVER_PORT exported, or a CI image that
// sets LOG_FORMAT, would otherwise change what every baseline test above reads —
// and the failure would appear on one machine and nowhere else, which is the
// exact bug this package is built around.
func TestIsolateClearsInheritedConfigEnv(t *testing.T) {
	t.Setenv("SERVER_PORT", "31337")
	t.Setenv("LOG_FORMAT", "json")
	t.Setenv(config.ConfigFileEnv, "/nonexistent/inherited.yaml")

	cfg, err := loadIn(t, t.TempDir())
	if err != nil {
		t.Fatalf("Load: %v — an inherited CONFIG_FILE was not cleared", err)
	}
	if cfg.Server.Port != 8080 {
		t.Errorf("server.port = %d, want the 8080 default — an inherited SERVER_PORT reached Load", cfg.Server.Port)
	}
	if cfg.Log.Format != config.FormatText {
		t.Errorf("log.format = %q, want the text default — an inherited LOG_FORMAT reached Load", cfg.Log.Format)
	}
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
