package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// GetEnv retrieves the value of the environment variable named by the key.
// If the variable is present in the environment, its value is returned.
// Otherwise, the provided defaultValue is returned.
func GetEnv(key, defaultValue string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultValue
}

// GetEnvAsInt retrieves the value of the environment variable named by the key
// and converts it to an integer. Returns defaultValue if unset or non-positive.
func GetEnvAsInt(key string, defaultValue int) int {
	if v, err := strconv.Atoi(os.Getenv(key)); err == nil && v > 0 {
		return v
	}
	return defaultValue
}

// GetEnvAsIntNonNeg retrieves the value of the environment variable and
// converts it to a non-negative integer. Returns defaultValue if unset or negative.
func GetEnvAsIntNonNeg(key string, defaultValue int) int {
	if v, err := strconv.Atoi(os.Getenv(key)); err == nil && v >= 0 {
		return v
	}
	return defaultValue
}

// GetEnvBool returns true if the environment variable is set to "true" or "1".
func GetEnvBool(key string) bool {
	v := strings.ToLower(os.Getenv(key))
	return v == "true" || v == "1"
}

// ---------------------------------------------------------------------------
// Backfill
// ---------------------------------------------------------------------------

// BackfillMaxDays returns the PR size backfill lookback window in days.
// Override: BACKFILL_MAX_DAYS (default 90).
func BackfillMaxDays() int { return GetEnvAsInt("BACKFILL_MAX_DAYS", 90) }

// ---------------------------------------------------------------------------
// Database pool
// ---------------------------------------------------------------------------

// DBMaxOpenConns returns the max open database connections.
// Override: DB_MAX_OPEN_CONNS. Caller supplies the per-pool default.
func DBMaxOpenConns(poolDefault int) int { return GetEnvAsInt("DB_MAX_OPEN_CONNS", poolDefault) }

// DBMaxIdleConns returns the max idle database connections.
// Override: DB_MAX_IDLE_CONNS. Caller supplies the per-pool default.
func DBMaxIdleConns(poolDefault int) int { return GetEnvAsInt("DB_MAX_IDLE_CONNS", poolDefault) }

// ---------------------------------------------------------------------------
// Import
// ---------------------------------------------------------------------------

// ImportMode returns the import mode (all, import, reputation).
// Override: IMPORT_MODE (default "all").
func ImportMode() string { return GetEnv("IMPORT_MODE", "all") }

// ImportWorkers returns the number of concurrent goroutine workers per task.
// Override: IMPORT_WORKERS (default 2).
func ImportWorkers() int { return GetEnvAsInt("IMPORT_WORKERS", 2) }

// ImportTaskTimeout returns the per-task timeout in minutes.
// Override: IMPORT_TASK_TIMEOUT (default 55).
func ImportTaskTimeout() int { return GetEnvAsInt("IMPORT_TASK_TIMEOUT", 55) }

// CloudRunTaskIndex returns the 0-based task index within the job execution.
// Override: CLOUD_RUN_TASK_INDEX (default 0 for local/single-task runs).
func CloudRunTaskIndex() int { return GetEnvAsIntNonNeg("CLOUD_RUN_TASK_INDEX", 0) }

// CloudRunTaskCount returns the total number of tasks in the job execution.
// Override: CLOUD_RUN_TASK_COUNT (default 1 for local/single-task runs).
func CloudRunTaskCount() int { return GetEnvAsInt("CLOUD_RUN_TASK_COUNT", 1) }

// CloudRunExecution returns the Cloud Run execution ID, or a local fallback.
// Override: CLOUD_RUN_EXECUTION.
func CloudRunExecution() string {
	return GetEnv("CLOUD_RUN_EXECUTION", fmt.Sprintf("local-%d", time.Now().Unix()))
}

// ---------------------------------------------------------------------------
// Anthropic / LLM
// ---------------------------------------------------------------------------

// AnthropicAPIKey returns the Anthropic API key (empty if not configured).
func AnthropicAPIKey() string { return os.Getenv("ANTHROPIC_API_KEY") }

// AnthropicBaseURL returns the optional Anthropic base URL override.
func AnthropicBaseURL() string { return os.Getenv("ANTHROPIC_BASE_URL") }

// AnthropicModel returns the Anthropic model for the given default.
// Both import insights and metrics review read ANTHROPIC_MODEL but use
// different defaults (haiku for high-volume insights, sonnet for review).
// Override: ANTHROPIC_MODEL.
func AnthropicModel(defaultModel string) string { return GetEnv("ANTHROPIC_MODEL", defaultModel) }

// ---------------------------------------------------------------------------
// GCP
// ---------------------------------------------------------------------------

// GCPProjectID returns the GCP project ID.
// Override: GCP_PROJECT_ID (default "devpulseio").
func GCPProjectID() string { return GetEnv("GCP_PROJECT_ID", "devpulseio") }

// ---------------------------------------------------------------------------
// Observability
// ---------------------------------------------------------------------------

// DebugEnabled returns true if debug logging is enabled.
// Override: DEVPULSE_DEBUG=true|1.
func DebugEnabled() bool { return GetEnvBool("DEVPULSE_DEBUG") }
