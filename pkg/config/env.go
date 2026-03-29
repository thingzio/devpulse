package config

import "os"

// GetEnv retrieves the value of the environment variable named by the key.
// If the variable is present in the environment, its value is returned.
// Otherwise, the provided defaultValue is returned.
func GetEnv(key, defaultValue string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return defaultValue
}
