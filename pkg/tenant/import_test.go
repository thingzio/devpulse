package tenant

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
