package config

import (
	"encoding/json"
	"log/slog"
)

// redacted is what a Secret renders as everywhere except Reveal.
const redacted = "***"

// Secret is a string that does not print itself.
//
// The failure it prevents is mundane and common: a config struct reaches a log
// line, an error message, or an /info response through %v, json.Marshal, or
// slog, and a password goes with it. Secret closes all three routes, so the
// only way to obtain the value is Reveal — which means `grep -rn '\.Reveal()'`
// is a complete audit of every place a secret is used.
//
// Use it for any field whose value should not appear in output:
//
//	Password config.Secret `mapstructure:"password" json:"password"`
//
// An empty Secret renders as empty rather than as ***, so operators can tell
// "not configured" from "configured, hidden" without exposing anything.
type Secret string

// String implements fmt.Stringer, covering %s, %v, and print.
func (s Secret) String() string {
	if s == "" {
		return ""
	}
	return redacted
}

// GoString covers %#v, which ignores String and would otherwise print the
// underlying value.
func (s Secret) GoString() string {
	if s == "" {
		return `""`
	}
	return `"` + redacted + `"`
}

// MarshalJSON covers encoding/json, and so any endpoint that serves the config.
func (s Secret) MarshalJSON() ([]byte, error) {
	return json.Marshal(s.String())
}

// LogValue implements slog.LogValuer, covering structured log records. slog
// calls this in preference to String, and would otherwise reach the underlying
// string through reflection.
func (s Secret) LogValue() slog.Value {
	return slog.StringValue(s.String())
}

// Reveal returns the underlying value. It is the only way to obtain it, which
// is the point: every use is greppable.
func (s Secret) Reveal() string { return string(s) }

// IsSet reports whether the secret has a value, without revealing it.
func (s Secret) IsSet() bool { return s != "" }
