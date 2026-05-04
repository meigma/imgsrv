package app

import (
	"fmt"
	"io"
	"log/slog"
	"strings"
)

// NewLogger constructs a slog logger from runtime logging configuration.
func NewLogger(w io.Writer, format string, level string) (*slog.Logger, error) {
	if w == nil {
		w = io.Discard
	}

	slogLevel, err := parseLogLevel(level)
	if err != nil {
		return nil, err
	}

	opts := &slog.HandlerOptions{Level: slogLevel}
	switch strings.ToLower(format) {
	case "text":
		return slog.New(slog.NewTextHandler(w, opts)), nil
	case "json":
		return slog.New(slog.NewJSONHandler(w, opts)), nil
	default:
		return nil, fmt.Errorf("unsupported log format %q", format)
	}
}

func parseLogLevel(level string) (slog.Level, error) {
	switch strings.ToLower(level) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return slog.LevelInfo, fmt.Errorf("unsupported log level %q", level)
	}
}
