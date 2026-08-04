// Package logging constructs the shared structured JSON logger.
package logging

import (
	"fmt"
	"io"
	"log/slog"
	"os"
)

// New creates a JSON logger carrying stable service and environment fields.
func New(service, environment, level string) (*slog.Logger, error) {
	return NewWithWriter(os.Stdout, service, environment, level)
}

// NewWithWriter is New with an injectable destination for tests.
func NewWithWriter(w io.Writer, service, environment, level string) (*slog.Logger, error) {
	parsed, err := parseLevel(level)
	if err != nil {
		return nil, err
	}

	handler := slog.NewJSONHandler(w, &slog.HandlerOptions{Level: parsed})
	return slog.New(handler).With(
		slog.String("service", service),
		slog.String("environment", environment),
	), nil
}

func parseLevel(level string) (slog.Level, error) {
	switch level {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("unsupported log level %q", level)
	}
}
