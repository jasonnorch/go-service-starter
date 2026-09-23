package postgres_test

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/jasonnorch/go-service-starter/internal/config"
	"github.com/jasonnorch/go-service-starter/internal/postgres"
)

func discard() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// Open wraps the driver's error, and app.Run logs it at startup. If the
// driver ever echoed the connection string back unredacted, the database
// password would land in the log of every failed start. Each URL here fails
// to parse, so the test needs no database and never dials anything.
func TestOpenDoesNotLeakThePasswordOnAParseError(t *testing.T) {
	const password = "hunter2-do-not-log"
	urls := []struct {
		name string
		url  string
	}{
		{"bad sslmode", "postgres://app:" + password + "@localhost:5432/app?sslmode=bogus"},
		{"space in host", "postgres://app:" + password + "@local host:5432/app"},
		{"non-numeric port", "postgres://app:" + password + "@localhost:notaport/app"},
		{"unclosed ipv6 bracket", "postgres://app:" + password + "@[::1/app"},
		{"keyword form", "host=localhost password=" + password + " sslmode=bogus"},
	}

	for _, tt := range urls {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.Postgres{
				URL:            config.Secret(tt.url),
				MaxConns:       1,
				ConnectTimeout: time.Second,
			}
			_, err := postgres.Open(context.Background(), cfg, discard())
			if err == nil {
				t.Fatal("Open accepted a malformed URL")
			}
			if strings.Contains(err.Error(), password) {
				t.Errorf("the password reached the error: %v", err)
			}
		})
	}
}
