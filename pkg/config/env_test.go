package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestImportOrg(t *testing.T) {
	t.Run("default empty", func(t *testing.T) {
		assert.Empty(t, ImportOrg())
	})
	t.Run("set", func(t *testing.T) {
		t.Setenv("IMPORT_ORG", "myorg")
		assert.Equal(t, "myorg", ImportOrg())
	})
}

func TestImportRepo(t *testing.T) {
	t.Run("default empty", func(t *testing.T) {
		assert.Empty(t, ImportRepo())
	})
	t.Run("set", func(t *testing.T) {
		t.Setenv("IMPORT_REPO", "myrepo")
		assert.Equal(t, "myrepo", ImportRepo())
	})
}

func TestImportAdoptTimeout(t *testing.T) {
	t.Run("default 60", func(t *testing.T) {
		assert.Equal(t, 60, ImportAdoptTimeout())
	})
	t.Run("custom", func(t *testing.T) {
		t.Setenv("IMPORT_ADOPT_TIMEOUT", "30")
		assert.Equal(t, 30, ImportAdoptTimeout())
	})
}

func TestImportJobName(t *testing.T) {
	t.Run("default empty", func(t *testing.T) {
		assert.Empty(t, ImportJobName())
	})
	t.Run("set", func(t *testing.T) {
		t.Setenv("IMPORT_JOB_NAME", "projects/p/locations/l/jobs/j")
		assert.Equal(t, "projects/p/locations/l/jobs/j", ImportJobName())
	})
}
