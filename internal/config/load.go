package config

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/spf13/viper"
)

// defaultsYAML is the complete set of built-in defaults, embedded so the binary
// is self-contained: it runs correctly from a cold clone with no config file,
// no environment, and no network.
//
//go:embed defaults.yaml
var defaultsYAML []byte

// DefaultConfigFile is the override file looked for in the working directory
// when CONFIG_FILE is not set. Its absence is not an error.
const DefaultConfigFile = "config.yaml"

// ConfigFileEnv names the environment variable that overrides which file is
// read. Unlike DefaultConfigFile, a file named here and missing IS an error —
// naming a file explicitly and having it silently ignored is how a deployment
// runs on defaults while everyone believes it is configured.
const ConfigFileEnv = "CONFIG_FILE"

// Load reads the three configuration layers and returns the result, validated.
//
// Later layers override earlier ones:
//
//  1. the embedded defaults
//  2. ./config.yaml, or $CONFIG_FILE — optional, override-only
//  3. the environment: server.read_timeout ← SERVER_READ_TIMEOUT
//
// There is no /etc search path, and no walk up the directory tree. Both are
// convenient and both mean a test's result depends on what the machine running
// it happens to have installed — which is a failure that reproduces on one
// laptop and nowhere else.
func Load() (*Config, error) {
	v := viper.New()
	v.SetConfigType("yaml")

	// Reading the embedded defaults does double duty. Besides supplying the
	// values, it registers every key with Viper — and that registration is what
	// makes AutomaticEnv work through Unmarshal. Unmarshal builds its input
	// from the known key set, so an environment variable for a key Viper has
	// never seen is not consulted at all. Registering the keys here is what
	// removes the need for a BindEnv call per field.
	if err := v.ReadConfig(bytes.NewReader(defaultsYAML)); err != nil {
		return nil, fmt.Errorf("reading embedded defaults: %w", err)
	}

	if path, explicit := configFilePath(); path != "" {
		if err := mergeFile(v, path, explicit); err != nil {
			return nil, err
		}
	}

	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("decoding configuration: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	return &cfg, nil
}

// configFilePath returns the override file to look for, and whether it was
// named explicitly through the environment.
func configFilePath() (path string, explicit bool) {
	if p := strings.TrimSpace(os.Getenv(ConfigFileEnv)); p != "" {
		return p, true
	}
	return DefaultConfigFile, false
}

// mergeFile layers path over what v already holds. A missing file is an error
// only when the operator named it themselves.
func mergeFile(v *viper.Viper, path string, explicit bool) error {
	f, err := os.Open(path) //nolint:gosec // the path is operator-supplied configuration, which is the point
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) && !explicit {
			return nil
		}
		return fmt.Errorf("opening config file %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	if err := v.MergeConfig(f); err != nil {
		return fmt.Errorf("reading config file %s: %w", path, err)
	}
	return nil
}
