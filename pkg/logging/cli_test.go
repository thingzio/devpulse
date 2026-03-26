package logging

import (
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSetupLogger(t *testing.T) {
	original := slog.Default()
	defer slog.SetDefault(original)

	SetupLogger()

	logger := slog.Default()
	assert.NotNil(t, logger)
	assert.IsType(t, &slog.JSONHandler{}, logger.Handler())
}

func TestParseLogLevel(t *testing.T) {
	tests := []struct {
		input string
		want  slog.Level
	}{
		{"debug", slog.LevelDebug},
		{"DEBUG", slog.LevelDebug},
		{"info", slog.LevelInfo},
		{"warn", slog.LevelWarn},
		{"warning", slog.LevelWarn},
		{"error", slog.LevelError},
		{"unknown", slog.LevelInfo},
		{"", slog.LevelInfo},
		{"  debug  ", slog.LevelDebug},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := ParseLogLevel(tt.input)
			assert.Equal(t, tt.want, got)
		})
	}
}
