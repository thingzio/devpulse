package tenant

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPrepareImportQueue(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	tn, err := UpsertTenant(ctx, db, 1001, "queueuser", "q@test.com", "")
	require.NoError(t, err)

	repos := []OrgRepo{
		{Org: "org1", Repo: "repo1"},
		{Org: "org1", Repo: "repo2"},
	}
	require.NoError(t, AddTenantRepos(ctx, db, tn.ID, repos))

	// Simulate a stale claim (older than 2h)
	_, err = db.ExecContext(ctx, `
		UPDATE tenant_repo
		SET import_claimed_at = NOW() - INTERVAL '3 hours', import_claimed_by = 'stale-exec'
		WHERE tenant_id = $1 AND org = 'org1' AND repo = 'repo1'`, tn.ID)
	require.NoError(t, err)

	// Simulate a completed repo
	_, err = db.ExecContext(ctx, `
		UPDATE tenant_repo
		SET import_done_at = NOW()
		WHERE tenant_id = $1 AND org = 'org1' AND repo = 'repo2'`, tn.ID)
	require.NoError(t, err)

	require.NoError(t, PrepareImportQueue(ctx, db))

	// Both repos should now be unclaimed and undone
	var staleClaims, doneClaims int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM tenant_repo WHERE tenant_id = $1 AND import_claimed_at IS NOT NULL`, tn.ID).Scan(&staleClaims))
	assert.Equal(t, 0, staleClaims, "stale claim should be reset")

	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM tenant_repo WHERE tenant_id = $1 AND import_done_at IS NOT NULL`, tn.ID).Scan(&doneClaims))
	assert.Equal(t, 0, doneClaims, "done marker should be reset")
}

func TestClaimNextRepo(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	tn, err := UpsertTenant(ctx, db, 2001, "claimuser", "c@test.com", "")
	require.NoError(t, err)

	repos := []OrgRepo{
		{Org: "myorg", Repo: "alpha"},
		{Org: "myorg", Repo: "beta"},
	}
	require.NoError(t, AddTenantRepos(ctx, db, tn.ID, repos))
	require.NoError(t, PrepareImportQueue(ctx, db))

	// Claim first repo
	claim1, err := ClaimNextRepo(ctx, db, "exec-1")
	require.NoError(t, err)
	require.NotNil(t, claim1)
	assert.Equal(t, tn.ID, claim1.TenantID)
	assert.Equal(t, "myorg", claim1.Org)
	assert.NotEmpty(t, claim1.ID)

	// Claim second repo
	claim2, err := ClaimNextRepo(ctx, db, "exec-1")
	require.NoError(t, err)
	require.NotNil(t, claim2)
	assert.NotEqual(t, claim1.Repo, claim2.Repo, "second claim should be a different repo")

	// Queue exhausted
	_, err = ClaimNextRepo(ctx, db, "exec-1")
	assert.True(t, errors.Is(err, ErrNoWork), "expected ErrNoWork, got %v", err)
}

func TestMarkRepoDone(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	tn, err := UpsertTenant(ctx, db, 3001, "doneuser", "d@test.com", "")
	require.NoError(t, err)

	require.NoError(t, AddTenantRepos(ctx, db, tn.ID, []OrgRepo{{Org: "org", Repo: "repo"}}))
	require.NoError(t, PrepareImportQueue(ctx, db))

	claim, err := ClaimNextRepo(ctx, db, "exec-done")
	require.NoError(t, err)
	require.NotNil(t, claim)

	require.NoError(t, MarkRepoDone(ctx, db, claim.ID))

	// Verify done_at is set
	var doneCount int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM tenant_repo WHERE tenant_id = $1 AND import_done_at IS NOT NULL`, tn.ID).Scan(&doneCount))
	assert.Equal(t, 1, doneCount)

	// PrepareImportQueue resets done repos for the next cycle
	require.NoError(t, PrepareImportQueue(ctx, db))
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM tenant_repo WHERE tenant_id = $1 AND import_done_at IS NOT NULL`, tn.ID).Scan(&doneCount))
	assert.Equal(t, 0, doneCount, "done marker should be cleared after PrepareImportQueue")
}

func TestClaimNextRepo_ConcurrentClaims(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	tn, err := UpsertTenant(ctx, db, 4001, "concuser", "con@test.com", "")
	require.NoError(t, err)

	const numRepos = 5
	repos := make([]OrgRepo, numRepos)
	for i := range repos {
		repos[i] = OrgRepo{Org: "concurrent", Repo: "repo" + string(rune('a'+i))}
	}
	require.NoError(t, AddTenantRepos(ctx, db, tn.ID, repos))
	require.NoError(t, PrepareImportQueue(ctx, db))

	// Two goroutines claim concurrently — each repo must be claimed exactly once.
	type result struct {
		claim *ClaimedRepo
		err   error
	}
	ch := make(chan result, numRepos*2)

	var wg sync.WaitGroup
	for g := 0; g < 2; g++ {
		wg.Add(1)
		execID := "exec-conc-" + string(rune('0'+g))
		go func(id string) {
			defer wg.Done()
			for {
				c, claimErr := ClaimNextRepo(ctx, db, id)
				if errors.Is(claimErr, ErrNoWork) {
					return
				}
				ch <- result{claim: c, err: claimErr}
			}
		}(execID)
	}
	wg.Wait()
	close(ch)

	seen := make(map[string]bool)
	for r := range ch {
		require.NoError(t, r.err)
		key := r.claim.Org + "/" + r.claim.Repo
		assert.False(t, seen[key], "repo %s claimed more than once", key)
		seen[key] = true
	}
	assert.Len(t, seen, numRepos, "all repos should be claimed exactly once")
}

func TestIncrementImportErrors(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	tn, err := UpsertTenant(ctx, db, 5001, "erruser", "e@test.com", "")
	require.NoError(t, err)

	require.NoError(t, AddTenantRepos(ctx, db, tn.ID, []OrgRepo{{Org: "org", Repo: "repo"}}))
	require.NoError(t, PrepareImportQueue(ctx, db))

	claim, err := ClaimNextRepo(ctx, db, "exec-err")
	require.NoError(t, err)

	for i := 1; i <= 3; i++ {
		require.NoError(t, IncrementImportErrors(ctx, db, claim.ID, "test error"))
		var count int
		require.NoError(t, db.QueryRowContext(ctx,
			`SELECT import_errors FROM tenant_repo WHERE id = $1`, claim.ID).Scan(&count))
		assert.Equal(t, i, count)
	}

	var lastErr string
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT COALESCE(import_last_error, '') FROM tenant_repo WHERE id = $1`, claim.ID).Scan(&lastErr))
	assert.Equal(t, "test error", lastErr)
}

func TestResetImportErrors(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	tn, err := UpsertTenant(ctx, db, 5002, "resetuser", "r@test.com", "")
	require.NoError(t, err)

	require.NoError(t, AddTenantRepos(ctx, db, tn.ID, []OrgRepo{{Org: "org", Repo: "repo"}}))
	require.NoError(t, PrepareImportQueue(ctx, db))

	claim, err := ClaimNextRepo(ctx, db, "exec-reset")
	require.NoError(t, err)

	require.NoError(t, IncrementImportErrors(ctx, db, claim.ID, "some error"))
	require.NoError(t, IncrementImportErrors(ctx, db, claim.ID, "another error"))

	require.NoError(t, ResetImportErrors(ctx, db, claim.ID))

	var count int
	var lastErr sql.NullString
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT import_errors, import_last_error FROM tenant_repo WHERE id = $1`, claim.ID).Scan(&count, &lastErr))
	assert.Equal(t, 0, count)
	assert.False(t, lastErr.Valid, "import_last_error should be NULL after reset")
}

func TestClaimNextRepo_SkipsPausedRepos(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	tn, err := UpsertTenant(ctx, db, 5003, "skipuser", "s@test.com", "")
	require.NoError(t, err)

	repos := []OrgRepo{
		{Org: "org", Repo: "broken"},
		{Org: "org", Repo: "healthy"},
	}
	require.NoError(t, AddTenantRepos(ctx, db, tn.ID, repos))

	_, err = db.ExecContext(ctx,
		`UPDATE tenant_repo SET import_errors = 5 WHERE tenant_id = $1 AND repo = 'broken'`, tn.ID)
	require.NoError(t, err)

	require.NoError(t, PrepareImportQueue(ctx, db))

	claim, err := ClaimNextRepo(ctx, db, "exec-skip")
	require.NoError(t, err)
	assert.Equal(t, "healthy", claim.Repo)

	_, err = ClaimNextRepo(ctx, db, "exec-skip")
	assert.True(t, errors.Is(err, ErrNoWork))
}
