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
