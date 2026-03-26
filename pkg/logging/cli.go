package logging

import (
	"log/slog"
	"os"
	"strings"
)

// SetupLogger configures the default slog logger with JSON output.
// Level is determined by the DEVPULSE_DEBUG env var.
func SetupLogger() {
	level := slog.LevelInfo
	if v := os.Getenv("DEVPULSE_DEBUG"); v == "true" || v == "1" {
		level = slog.LevelDebug
	}
	handler := slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level})
	slog.SetDefault(slog.New(handler))
}

// ParseLogLevel converts a string log level to slog.Level.
func ParseLogLevel(level string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
