package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetEnv(t *testing.T) {
	t.Run("returns value when set", func(t *testing.T) {
		t.Setenv("TEST_KEY_ABC", "hello")
		assert.Equal(t, "hello", GetEnv("TEST_KEY_ABC", "default"))
	})

	t.Run("returns default when not set", func(t *testing.T) {
		assert.Equal(t, "default", GetEnv("TEST_KEY_UNSET_XYZ", "default"))
	})

	t.Run("returns empty string default when not set", func(t *testing.T) {
		assert.Equal(t, "", GetEnv("TEST_KEY_UNSET_XYZ", ""))
	})

	t.Run("returns default when set to empty", func(t *testing.T) {
		t.Setenv("TEST_KEY_EMPTY", "")
		assert.Equal(t, "default", GetEnv("TEST_KEY_EMPTY", "default"))
	})
}

func TestGetEnvAsInt(t *testing.T) {
	t.Run("returns parsed int when set", func(t *testing.T) {
		t.Setenv("TEST_INT", "42")
		assert.Equal(t, 42, GetEnvAsInt("TEST_INT", 10))
	})

	t.Run("returns default when not set", func(t *testing.T) {
		assert.Equal(t, 10, GetEnvAsInt("TEST_INT_UNSET", 10))
	})

	t.Run("returns default for non-numeric", func(t *testing.T) {
		t.Setenv("TEST_INT_BAD", "abc")
		assert.Equal(t, 10, GetEnvAsInt("TEST_INT_BAD", 10))
	})

	t.Run("returns default for zero", func(t *testing.T) {
		t.Setenv("TEST_INT_ZERO", "0")
		assert.Equal(t, 10, GetEnvAsInt("TEST_INT_ZERO", 10))
	})

	t.Run("returns default for negative", func(t *testing.T) {
		t.Setenv("TEST_INT_NEG", "-5")
		assert.Equal(t, 10, GetEnvAsInt("TEST_INT_NEG", 10))
	})
}

func TestGetEnvBool(t *testing.T) {
	t.Run("true for 'true'", func(t *testing.T) {
		t.Setenv("TEST_BOOL", "true")
		assert.True(t, GetEnvBool("TEST_BOOL"))
	})

	t.Run("true for '1'", func(t *testing.T) {
		t.Setenv("TEST_BOOL", "1")
		assert.True(t, GetEnvBool("TEST_BOOL"))
	})

	t.Run("true for 'TRUE'", func(t *testing.T) {
		t.Setenv("TEST_BOOL", "TRUE")
		assert.True(t, GetEnvBool("TEST_BOOL"))
	})

	t.Run("false when not set", func(t *testing.T) {
		assert.False(t, GetEnvBool("TEST_BOOL_UNSET"))
	})

	t.Run("false for other values", func(t *testing.T) {
		t.Setenv("TEST_BOOL", "yes")
		assert.False(t, GetEnvBool("TEST_BOOL"))
	})
}

func TestBackfillMaxDays(t *testing.T) {
	t.Run("returns default", func(t *testing.T) {
		t.Setenv("BACKFILL_MAX_DAYS", "")
		assert.Equal(t, 90, BackfillMaxDays())
	})

	t.Run("returns override", func(t *testing.T) {
		t.Setenv("BACKFILL_MAX_DAYS", "180")
		assert.Equal(t, 180, BackfillMaxDays())
	})
}

func TestImportResetMinutes(t *testing.T) {
	t.Run("returns default", func(t *testing.T) {
		t.Setenv("IMPORT_RESET_MINUTES", "")
		assert.Equal(t, 30, ImportResetMinutes())
	})

	t.Run("returns override", func(t *testing.T) {
		t.Setenv("IMPORT_RESET_MINUTES", "60")
		assert.Equal(t, 60, ImportResetMinutes())
	})
}

func TestAnthropicModel(t *testing.T) {
	t.Run("returns default when not set", func(t *testing.T) {
		assert.Equal(t, "claude-haiku-4-5-20251001", AnthropicModel("claude-haiku-4-5-20251001"))
	})

	t.Run("returns override", func(t *testing.T) {
		t.Setenv("ANTHROPIC_MODEL", "claude-sonnet-4-6")
		assert.Equal(t, "claude-sonnet-4-6", AnthropicModel("claude-haiku-4-5-20251001"))
	})
}

func TestGCPProjectID(t *testing.T) {
	t.Run("returns default", func(t *testing.T) {
		assert.Equal(t, "devpulseio", GCPProjectID())
	})

	t.Run("returns override", func(t *testing.T) {
		t.Setenv("GCP_PROJECT_ID", "my-project")
		assert.Equal(t, "my-project", GCPProjectID())
	})
}

func TestDebugEnabled(t *testing.T) {
	t.Run("false when not set", func(t *testing.T) {
		assert.False(t, DebugEnabled())
	})

	t.Run("true when set", func(t *testing.T) {
		t.Setenv("DEVPULSE_DEBUG", "true")
		assert.True(t, DebugEnabled())
	})
}
