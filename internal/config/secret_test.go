package config_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/jasonnorch/go-service-starter/internal/config"
)

const sentinel = "hunter2-do-not-print-me"

// Each of these is a real route by which a config struct has leaked a password
// into output. The test is the guarantee that all of them stay closed.
func TestSecretIsRedactedEverywhere(t *testing.T) {
	s := config.Secret(sentinel)

	t.Run("fmt", func(t *testing.T) {
		for _, verb := range []string{"%s", "%v", "%q", "%#v", "%+v"} {
			out := fmt.Sprintf(verb, s)
			if strings.Contains(out, sentinel) {
				t.Errorf("%s leaked the value: %s", verb, out)
			}
		}
	})

	t.Run("json", func(t *testing.T) {
		// Marshaled inside a struct, which is how it actually reaches an
		// /info response — and is the case a value-receiver MarshalJSON can
		// miss if it is ever changed to a pointer receiver.
		out, err := json.Marshal(struct {
			Password config.Secret `json:"password"`
		}{s})
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		if bytes.Contains(out, []byte(sentinel)) {
			t.Errorf("json leaked the value: %s", out)
		}
	})

	t.Run("slog", func(t *testing.T) {
		var buf bytes.Buffer
		slog.New(slog.NewJSONHandler(&buf, nil)).Info("connecting", "password", s)
		if strings.Contains(buf.String(), sentinel) {
			t.Errorf("slog leaked the value: %s", buf.String())
		}
	})
}

func TestSecretReveal(t *testing.T) {
	if got := config.Secret(sentinel).Reveal(); got != sentinel {
		t.Errorf("Reveal() = %q, want the underlying value", got)
	}
}

// Empty renders as empty rather than ***, so an operator can tell "not
// configured" from "configured, hidden" without the value being exposed.
func TestSecretEmptyIsDistinguishable(t *testing.T) {
	var empty config.Secret

	if empty.String() != "" {
		t.Errorf("empty String() = %q, want empty", empty.String())
	}
	if empty.IsSet() {
		t.Error("empty IsSet() = true")
	}
	if !config.Secret(sentinel).IsSet() {
		t.Error("set IsSet() = false")
	}
	if config.Secret(sentinel).String() == "" {
		t.Error("set String() is empty, which would read as not configured")
	}
}
