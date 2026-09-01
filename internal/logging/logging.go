// Package logging builds the service's *slog.Logger.
//
// It is the only place a log handler is constructed, which is what makes the
// logging backend a one-file swap: slog is an interface with a standard
// handler contract, so replacing this file's slog.NewJSONHandler with a
// zap-backed, ECS, or OpenTelemetry handler changes the output format without
// touching a single call site.
//
// There is no package-level logger and no Default() here on purpose. A logger
// reached through a global cannot be replaced in a test without replacing it
// for every other test running concurrently, which is what forces the
// *WithLogger() constructor variants that appear in codebases that started with
// a global. Pass the *slog.Logger as a parameter instead.
package logging

import (
	"io"
	"log/slog"

	"github.com/jasonnorch/go-service-starter/internal/config"
)

// New returns a logger writing to w, configured by cfg.
//
// cfg is expected to have been through config.Validate, which restricts Level
// and Format to known values. New does not fail on an unknown one — a service
// that will not start because it cannot decide how to log is worse than one
// that logs at info — but it says so on the first line of output rather than
// silently choosing for you.
func New(cfg config.Log, w io.Writer) *slog.Logger {
	var level slog.Level
	levelErr := level.UnmarshalText([]byte(cfg.Level))
	if levelErr != nil {
		level = slog.LevelInfo
	}

	opts := &slog.HandlerOptions{
		Level: level,
		// Resolving the source costs a runtime.CallersFrames walk per record,
		// which is why it is off by default rather than always on.
		AddSource: cfg.AddSource,
	}

	var handler slog.Handler
	switch cfg.Format {
	case config.FormatJSON:
		handler = slog.NewJSONHandler(w, opts)
	default:
		handler = slog.NewTextHandler(w, opts)
	}

	logger := slog.New(handler)
	if levelErr != nil {
		logger.Warn("unknown log level, defaulting to info", "configured", cfg.Level)
	}
	return logger
}

// Bootstrap returns the logger used before configuration has been loaded — so
// that a failure to load it has somewhere to go. It is deliberately not
// configurable: whatever would configure it is the thing that has not loaded.
func Bootstrap(w io.Writer) *slog.Logger {
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: slog.LevelInfo}))
}
