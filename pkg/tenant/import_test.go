package tenant

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSanitizeImportError(t *testing.T) {
	t.Run("token redacted", func(t *testing.T) {
		got := sanitizeImportError("auth fail: ghp_abcdefghijklmnopqrstuvwxyz1234567890")
		assert.Contains(t, got, "[REDACTED]")
		assert.NotContains(t, got, "ghp_abcdefghijklmnopqrstuvwxyz1234567890")
	})
	t.Run("installation token redacted", func(t *testing.T) {
		got := sanitizeImportError("error: ghs_xyz1234567890abcdefghijklmnopqrstuv")
		assert.NotContains(t, got, "ghs_xyz1234567890abcdefghijklmnopqrstuv")
	})
	t.Run("bearer token redacted", func(t *testing.T) {
		got := sanitizeImportError("403: Bearer eyJhbGciOiJIUzI1NiJ9.payload.sig")
		assert.Contains(t, got, "Bearer [REDACTED]")
		assert.NotContains(t, got, "eyJhbGciOiJIUzI1NiJ9")
	})
	t.Run("authorization header redacted", func(t *testing.T) {
		got := sanitizeImportError("Authorization: token abcdef123")
		assert.Contains(t, got, "[REDACTED]")
		assert.NotContains(t, got, "abcdef123")
	})
	t.Run("truncated when long", func(t *testing.T) {
		long := strings.Repeat("x", 2048)
		got := sanitizeImportError(long)
		assert.LessOrEqual(t, len(got), importErrorMaxLen+len("...[truncated]"))
		assert.True(t, strings.HasSuffix(got, "...[truncated]"))
	})
	t.Run("short message untouched", func(t *testing.T) {
		got := sanitizeImportError("connection refused")
		assert.Equal(t, "connection refused", got)
	})
}

func TestIncrementImportErrors(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	tn, err := UpsertTenant(ctx, db, 5001, "erruser", "e@test.com", "", "", "", "", "")
	require.NoError(t, err)

	require.NoError(t, AddTenantRepos(ctx, db, tn.ID, []OrgRepo{{Org: "org", Repo: "repo"}}))

	var repoID string
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT id FROM devpulse_tenant_repo WHERE tenant_id = $1 AND org = 'org' AND repo = 'repo'`, tn.ID).Scan(&repoID))

	for i := 1; i <= 3; i++ {
		require.NoError(t, IncrementImportErrors(ctx, db, repoID, "test error"))
		var count int
		require.NoError(t, db.QueryRowContext(ctx,
			`SELECT import_errors FROM devpulse_tenant_repo WHERE id = $1`, repoID).Scan(&count))
		assert.Equal(t, i, count)
	}

	var lastErr string
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT COALESCE(import_last_error, '') FROM devpulse_tenant_repo WHERE id = $1`, repoID).Scan(&lastErr))
	assert.Equal(t, "test error", lastErr)
}

func TestResetImportErrors(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	tn, err := UpsertTenant(ctx, db, 5002, "resetuser", "r@test.com", "", "", "", "", "")
	require.NoError(t, err)

	require.NoError(t, AddTenantRepos(ctx, db, tn.ID, []OrgRepo{{Org: "org", Repo: "repo"}}))

	var repoID string
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT id FROM devpulse_tenant_repo WHERE tenant_id = $1 AND org = 'org' AND repo = 'repo'`, tn.ID).Scan(&repoID))

	require.NoError(t, IncrementImportErrors(ctx, db, repoID, "some error"))
	require.NoError(t, IncrementImportErrors(ctx, db, repoID, "another error"))

	require.NoError(t, ResetImportErrors(ctx, db, repoID))

	var count int
	var lastErr sql.NullString
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT import_errors, import_last_error FROM devpulse_tenant_repo WHERE id = $1`, repoID).Scan(&count, &lastErr))
	assert.Equal(t, 0, count)
	assert.False(t, lastErr.Valid, "import_last_error should be NULL after reset")
}
