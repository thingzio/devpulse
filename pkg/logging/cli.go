package logging

import (
	"log/slog"
	"os"

	"github.com/thingzio/devpulse/pkg/config"
)

// SetupLogger configures the default slog logger with JSON output.
// Level is determined by the DEVPULSE_DEBUG env var.
func SetupLogger() {
	level := slog.LevelInfo
	if config.DebugEnabled() {
		level = slog.LevelDebug
	}
	handler := slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level})
	slog.SetDefault(slog.New(handler))
}
