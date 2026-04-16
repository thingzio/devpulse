package logging

import (
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSetupLogger(t *testing.T) {
	original := slog.Default()
	defer slog.SetDefault(original)

	SetupLogger("v0.0.1-test")

	logger := slog.Default()
	assert.NotNil(t, logger)
}
