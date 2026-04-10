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

func TestGetEnvAsIntNonNeg(t *testing.T) {
	t.Run("returns default when not set", func(t *testing.T) {
		assert.Equal(t, 42, GetEnvAsIntNonNeg("UNSET_VAR_XYZ", 42))
	})

	t.Run("accepts zero", func(t *testing.T) {
		t.Setenv("TEST_NONNEG", "0")
		assert.Equal(t, 0, GetEnvAsIntNonNeg("TEST_NONNEG", 99))
	})

	t.Run("returns default for negative", func(t *testing.T) {
		t.Setenv("TEST_NONNEG", "-1")
		assert.Equal(t, 99, GetEnvAsIntNonNeg("TEST_NONNEG", 99))
	})

	t.Run("returns default for non-numeric", func(t *testing.T) {
		t.Setenv("TEST_NONNEG", "abc")
		assert.Equal(t, 99, GetEnvAsIntNonNeg("TEST_NONNEG", 99))
	})

	t.Run("accepts positive", func(t *testing.T) {
		t.Setenv("TEST_NONNEG", "5")
		assert.Equal(t, 5, GetEnvAsIntNonNeg("TEST_NONNEG", 99))
	})
}

func TestImportWorkers(t *testing.T) {
	t.Run("returns default", func(t *testing.T) {
		assert.Equal(t, 2, ImportWorkers())
	})

	t.Run("returns override", func(t *testing.T) {
		t.Setenv("IMPORT_WORKERS", "5")
		assert.Equal(t, 5, ImportWorkers())
	})

	t.Run("non-positive falls back to default", func(t *testing.T) {
		t.Setenv("IMPORT_WORKERS", "0")
		assert.Equal(t, 2, ImportWorkers())
	})
}

func TestImportTaskTimeout(t *testing.T) {
	t.Run("returns default", func(t *testing.T) {
		assert.Equal(t, 55, ImportTaskTimeout())
	})

	t.Run("returns override", func(t *testing.T) {
		t.Setenv("IMPORT_TASK_TIMEOUT", "30")
		assert.Equal(t, 30, ImportTaskTimeout())
	})
}

func TestCloudRunTaskIndex(t *testing.T) {
	t.Run("returns default", func(t *testing.T) {
		assert.Equal(t, 0, CloudRunTaskIndex())
	})

	t.Run("returns override", func(t *testing.T) {
		t.Setenv("CLOUD_RUN_TASK_INDEX", "2")
		assert.Equal(t, 2, CloudRunTaskIndex())
	})

	t.Run("zero is valid", func(t *testing.T) {
		t.Setenv("CLOUD_RUN_TASK_INDEX", "0")
		assert.Equal(t, 0, CloudRunTaskIndex())
	})
}

func TestCloudRunTaskCount(t *testing.T) {
	t.Run("returns default", func(t *testing.T) {
		assert.Equal(t, 1, CloudRunTaskCount())
	})

	t.Run("returns override", func(t *testing.T) {
		t.Setenv("CLOUD_RUN_TASK_COUNT", "3")
		assert.Equal(t, 3, CloudRunTaskCount())
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

func TestDeepRepLowScoreStaleHours(t *testing.T) {
	assert.Equal(t, 168, DeepRepLowScoreStaleHours())
	t.Setenv("DEEPREP_LOW_STALE_HOURS", "48")
	assert.Equal(t, 48, DeepRepLowScoreStaleHours())
}

func TestDeepRepHighScoreStaleHours(t *testing.T) {
	assert.Equal(t, 720, DeepRepHighScoreStaleHours())
	t.Setenv("DEEPREP_HIGH_STALE_HOURS", "336")
	assert.Equal(t, 336, DeepRepHighScoreStaleHours())
}

func TestDeepRepScoreThreshold(t *testing.T) {
	assert.Equal(t, 0.5, DeepRepScoreThreshold())
	t.Setenv("DEEPREP_SCORE_THRESHOLD", "0.7")
	assert.Equal(t, 0.7, DeepRepScoreThreshold())
}

func TestGetEnvAsFloat(t *testing.T) {
	assert.Equal(t, 3.14, GetEnvAsFloat("UNSET_FLOAT_XYZ", 3.14))
	t.Setenv("TEST_FLOAT", "0.75")
	assert.Equal(t, 0.75, GetEnvAsFloat("TEST_FLOAT", 1.0))
	t.Setenv("TEST_FLOAT", "-1.0")
	assert.Equal(t, 1.0, GetEnvAsFloat("TEST_FLOAT", 1.0), "negative should fall back")
	t.Setenv("TEST_FLOAT", "abc")
	assert.Equal(t, 1.0, GetEnvAsFloat("TEST_FLOAT", 1.0), "non-numeric should fall back")
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
