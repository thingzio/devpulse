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

	t.Run("returns empty string value when set to empty", func(t *testing.T) {
		t.Setenv("TEST_KEY_EMPTY", "")
		assert.Equal(t, "", GetEnv("TEST_KEY_EMPTY", "default"))
	})
}
