package logging

import (
	"log/slog"
	"os"
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
