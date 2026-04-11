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

// GetEnvAsFloat retrieves the value of the environment variable and
// converts it to a float64. Returns defaultValue if unset or invalid.
func GetEnvAsFloat(key string, defaultValue float64) float64 {
	if v, err := strconv.ParseFloat(os.Getenv(key), 64); err == nil && v > 0 {
		return v
	}
	return defaultValue
}

// ---------------------------------------------------------------------------
// Backfill
// ---------------------------------------------------------------------------

// BackfillMaxDays returns the PR size backfill lookback window in days.
// Override: BACKFILL_MAX_DAYS (default 90).
func BackfillMaxDays() int { return GetEnvAsInt("BACKFILL_MAX_DAYS", 90) }

// ---------------------------------------------------------------------------
// Deep Reputation
// ---------------------------------------------------------------------------

// DeepRepLowScoreStaleHours returns the rescore interval for contributors
// with reputation below the score threshold. Override: DEEPREP_LOW_STALE_HOURS (default 168 = 7 days).
func DeepRepLowScoreStaleHours() int { return GetEnvAsInt("DEEPREP_LOW_STALE_HOURS", 168) }

// DeepRepHighScoreStaleHours returns the rescore interval for contributors
// with reputation at or above the score threshold. Override: DEEPREP_HIGH_STALE_HOURS (default 720 = 30 days).
func DeepRepHighScoreStaleHours() int { return GetEnvAsInt("DEEPREP_HIGH_STALE_HOURS", 720) }

// DeepRepScoreThreshold returns the reputation score boundary between low and
// high rescore tiers. Override: DEEPREP_SCORE_THRESHOLD (default 0.5).
func DeepRepScoreThreshold() float64 { return GetEnvAsFloat("DEEPREP_SCORE_THRESHOLD", 0.5) }

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

// ImportWorkers returns the number of concurrent goroutine workers per task.
// Override: IMPORT_WORKERS (default 2).
func ImportWorkers() int { return GetEnvAsInt("IMPORT_WORKERS", 2) }

// ImportTaskTimeout returns the per-task timeout in minutes.
// Override: IMPORT_TASK_TIMEOUT (default 55).
func ImportTaskTimeout() int { return GetEnvAsInt("IMPORT_TASK_TIMEOUT", 55) }

// ImportOrg returns the org for single-repo import mode.
// Override: IMPORT_ORG (default "").
func ImportOrg() string { return os.Getenv("IMPORT_ORG") }

// ImportRepo returns the repo for single-repo import mode.
// Override: IMPORT_REPO (default "").
func ImportRepo() string { return os.Getenv("IMPORT_REPO") }

// ImportAdoptTimeout returns the minutes before a new repo (import_done_at IS NULL)
// is adopted by the scheduled import as a safety net.
// Override: IMPORT_ADOPT_TIMEOUT (default 60).
func ImportAdoptTimeout() int { return GetEnvAsInt("IMPORT_ADOPT_TIMEOUT", 60) }

// ImportJobName returns the fully-qualified Cloud Run import job name for triggering on-demand imports.
// Override: IMPORT_JOB_NAME (default "").
func ImportJobName() string { return os.Getenv("IMPORT_JOB_NAME") }

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
// Admin
// ---------------------------------------------------------------------------

// AdminRequireIAM returns true if the admin server should verify Cloud Run
// IAM auth headers are present on every request (defense-in-depth).
// Override: ADMIN_REQUIRE_IAM (default true). Set to "false" for local dev.
func AdminRequireIAM() bool {
	v := strings.ToLower(os.Getenv("ADMIN_REQUIRE_IAM"))
	if v == "false" || v == "0" {
		return false
	}
	return true // default: require IAM
}

// ---------------------------------------------------------------------------
// Server
// ---------------------------------------------------------------------------

// ServerShutdownTimeout returns the graceful shutdown timeout.
// Override: SERVER_SHUTDOWN_TIMEOUT_SEC (default 5).
func ServerShutdownTimeout() int { return GetEnvAsInt("SERVER_SHUTDOWN_TIMEOUT_SEC", 5) }

// ServerTriggerTimeout returns the timeout for fire-and-forget import triggers.
// Override: SERVER_TRIGGER_TIMEOUT_SEC (default 30).
func ServerTriggerTimeout() int { return GetEnvAsInt("SERVER_TRIGGER_TIMEOUT_SEC", 30) }

// OAuthRateLimit returns the max OAuth requests per minute per IP.
// Override: OAUTH_RATE_LIMIT (default 20).
func OAuthRateLimit() int { return GetEnvAsInt("OAUTH_RATE_LIMIT", 20) }

// RepoSearchRateLimit returns the max repo-search requests per minute per IP.
// Override: REPO_SEARCH_RATE_LIMIT (default 30).
func RepoSearchRateLimit() int { return GetEnvAsInt("REPO_SEARCH_RATE_LIMIT", 30) }

// QueryParamMinDays returns the minimum allowed value for the days query parameter.
// Override: QUERY_PARAM_MIN_DAYS (default 14).
func QueryParamMinDays() int { return GetEnvAsInt("QUERY_PARAM_MIN_DAYS", 14) }

// QueryParamMaxDays returns the maximum allowed value for the days query parameter.
// Override: QUERY_PARAM_MAX_DAYS (default 3650).
func QueryParamMaxDays() int { return GetEnvAsInt("QUERY_PARAM_MAX_DAYS", 3650) }

// ---------------------------------------------------------------------------
// HTTP Transport
// ---------------------------------------------------------------------------

// HTTPMaxIdleConns returns the max idle connections for the shared HTTP transport.
// Override: HTTP_MAX_IDLE_CONNS (default 50).
func HTTPMaxIdleConns() int { return GetEnvAsInt("HTTP_MAX_IDLE_CONNS", 50) }

// HTTPMaxIdleConnsPerHost returns the max idle connections per host.
// Override: HTTP_MAX_IDLE_CONNS_PER_HOST (default 10).
func HTTPMaxIdleConnsPerHost() int { return GetEnvAsInt("HTTP_MAX_IDLE_CONNS_PER_HOST", 10) }

// ---------------------------------------------------------------------------
// Observability
// ---------------------------------------------------------------------------

// DebugEnabled returns true if debug logging is enabled.
// Override: DEVPULSE_DEBUG=true|1.
func DebugEnabled() bool { return GetEnvBool("DEVPULSE_DEBUG") }
