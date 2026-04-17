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

func TestServerConfig(t *testing.T) {
	t.Run("shutdown timeout default", func(t *testing.T) {
		assert.Equal(t, 5, ServerShutdownTimeout())
	})
	t.Run("trigger timeout default", func(t *testing.T) {
		assert.Equal(t, 30, ServerTriggerTimeout())
	})
	t.Run("oauth rate limit default", func(t *testing.T) {
		assert.Equal(t, 20, OAuthRateLimit())
	})
	t.Run("repo search rate limit default", func(t *testing.T) {
		assert.Equal(t, 30, RepoSearchRateLimit())
	})
	t.Run("query param min days default", func(t *testing.T) {
		assert.Equal(t, 14, QueryParamMinDays())
	})
	t.Run("query param max days default", func(t *testing.T) {
		assert.Equal(t, 3650, QueryParamMaxDays())
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
