package config

import (
	"os"
	"strconv"
)

// GetEnv retrieves the value of the environment variable named by the key.
// If the variable is present in the environment, its value is returned.
// Otherwise, the provided defaultValue is returned.
func GetEnv(key, defaultValue string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return defaultValue
}

const (
	// BackfillMaxDaysDefault is the default lookback window (in days) for PR
	// size backfill. Override at runtime with BACKFILL_MAX_DAYS env var.
	BackfillMaxDaysDefault = 90
	BackfillMaxDaysEnvKey  = "BACKFILL_MAX_DAYS"
)

// BackfillMaxDays returns the configured backfill window in days.
func BackfillMaxDays() int {
	if v, err := strconv.Atoi(os.Getenv(BackfillMaxDaysEnvKey)); err == nil && v > 0 {
		return v
	}
	return BackfillMaxDaysDefault
}
