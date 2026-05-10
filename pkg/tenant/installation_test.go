package tenant

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSaveAndListInstallations(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	tn, err := UpsertTenant(ctx, db, 60001, "installuser", "i@test.com", "", "", "", "", "")
	require.NoError(t, err)

	perms, err := json.Marshal(map[string]string{"contents": "read"})
	require.NoError(t, err)

	require.NoError(t, SaveInstallation(ctx, db, tn.ID, 1001, "Organization", "myorg", perms, 100))
	require.NoError(t, SaveInstallation(ctx, db, tn.ID, 1002, "User", "myuser", nil, 100))

	list, err := ListInstallations(ctx, db, tn.ID)
	require.NoError(t, err)
	require.Len(t, list, 2)

	assert.Equal(t, int64(1001), list[0].InstallationID)
	assert.Equal(t, "Organization", list[0].TargetType)
	assert.Equal(t, "myorg", list[0].TargetLogin)
	assert.Nil(t, list[0].SuspendedAt)

	assert.Equal(t, int64(1002), list[1].InstallationID)
	assert.Equal(t, "User", list[1].TargetType)
}

func TestSaveInstallation_Upsert(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	tn, err := UpsertTenant(ctx, db, 60002, "upsertinstall", "", "", "", "", "", "")
	require.NoError(t, err)

	require.NoError(t, SaveInstallation(ctx, db, tn.ID, 2001, "Organization", "oldlogin", nil, 100))

	// Upsert same installation_id with new target_login
	require.NoError(t, SaveInstallation(ctx, db, tn.ID, 2001, "Organization", "newlogin", nil, 100))

	list, err := ListInstallations(ctx, db, tn.ID)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, "newlogin", list[0].TargetLogin)
}

func TestSuspendInstallation(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	tn, err := UpsertTenant(ctx, db, 60003, "suspenduser", "", "", "", "", "", "")
	require.NoError(t, err)

	require.NoError(t, SaveInstallation(ctx, db, tn.ID, 3001, "Organization", "org1", nil, 100))

	require.NoError(t, SuspendInstallation(ctx, db, 3001))

	list, err := ListInstallations(ctx, db, tn.ID)
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.NotNil(t, list[0].SuspendedAt)
}

// TestSuspendInstallation_RetryPreservesTimestamp guards the webhook
// idempotency fix: GitHub retries failed suspend webhook deliveries, so
// repeated SuspendInstallation calls must keep the ORIGINAL suspended_at
// instead of rewriting it on each retry.
func TestSuspendInstallation_RetryPreservesTimestamp(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	tn, err := UpsertTenant(ctx, db, 60013, "retrysuspend", "", "", "", "", "", "")
	require.NoError(t, err)
	require.NoError(t, SaveInstallation(ctx, db, tn.ID, 3013, "Organization", "org13", nil, 100))

	// First suspend.
	require.NoError(t, SuspendInstallation(ctx, db, 3013))
	first, err := ListInstallations(ctx, db, tn.ID)
	require.NoError(t, err)
	require.Len(t, first, 1)
	require.NotNil(t, first[0].SuspendedAt)
	originalSuspendedAt := *first[0].SuspendedAt

	// Wait long enough that NOW() would advance noticeably if the SQL
	// reset it on every retry.
	time.Sleep(50 * time.Millisecond)

	// Retry the suspend (mimics GitHub's redelivery).
	require.NoError(t, SuspendInstallation(ctx, db, 3013))
	second, err := ListInstallations(ctx, db, tn.ID)
	require.NoError(t, err)
	require.Len(t, second, 1)
	require.NotNil(t, second[0].SuspendedAt)

	assert.Equal(t, originalSuspendedAt.UnixNano(), second[0].SuspendedAt.UnixNano(),
		"suspended_at must be pinned to the first suspension; retry must not rewrite it")
}

func TestGetInstallationForOrg(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	tn, err := UpsertTenant(ctx, db, 60004, "orginstall", "", "", "", "", "", "")
	require.NoError(t, err)

	require.NoError(t, SaveInstallation(ctx, db, tn.ID, 4001, "Organization", "activeorg", nil, 100))
	require.NoError(t, SaveInstallation(ctx, db, tn.ID, 4002, "Organization", "suspendorg", nil, 100))
	require.NoError(t, SuspendInstallation(ctx, db, 4002))

	// Active installation found
	inst, err := GetInstallationForOrg(ctx, db, tn.ID, "activeorg", 100)
	require.NoError(t, err)
	require.NotNil(t, inst)
	assert.Equal(t, int64(4001), inst.ID)
	assert.Equal(t, "activeorg", inst.Login)

	// Suspended installation not returned
	inst, err = GetInstallationForOrg(ctx, db, tn.ID, "suspendorg", 100)
	require.NoError(t, err)
	assert.Nil(t, inst)

	// Non-existent org returns nil
	inst, err = GetInstallationForOrg(ctx, db, tn.ID, "noorg", 100)
	require.NoError(t, err)
	assert.Nil(t, inst)
}

func TestAddAndListTenantRepos(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	tn, err := UpsertTenant(ctx, db, 60005, "repouser", "", "", "", "", "", "")
	require.NoError(t, err)

	repos := []OrgRepo{
		{Org: "org1", Repo: "alpha"},
		{Org: "org1", Repo: "beta"},
		{Org: "org2", Repo: "gamma"},
	}
	require.NoError(t, AddTenantRepos(ctx, db, tn.ID, repos))

	list, err := ListTenantRepos(ctx, db, tn.ID)
	require.NoError(t, err)
	require.Len(t, list, 3)

	assert.Equal(t, "org1", list[0].Org)
	assert.Equal(t, "alpha", list[0].Repo)
	assert.True(t, list[0].Active)
	assert.Equal(t, tn.ID, list[0].TenantID)
}

func TestDeactivateTenantRepo(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	tn, err := UpsertTenant(ctx, db, 60006, "deactivateuser", "", "", "", "", "", "")
	require.NoError(t, err)

	repos := []OrgRepo{
		{Org: "org1", Repo: "keep"},
		{Org: "org1", Repo: "remove"},
	}
	require.NoError(t, AddTenantRepos(ctx, db, tn.ID, repos))

	require.NoError(t, DeactivateTenantRepo(ctx, db, tn.ID, "org1", "remove"))

	list, err := ListTenantRepos(ctx, db, tn.ID)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, "keep", list[0].Repo)
}

// TestDeactivateTenantRepo_TenantIsolation verifies that deactivating a repo
// for one tenant does not affect another tenant's row for the same org/repo.
// Defense-in-depth check on top of RLS — the SQL itself must filter by tenant_id.
func TestDeactivateTenantRepo_TenantIsolation(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	tn1, err := UpsertTenant(ctx, db, 60050, "isouser1", "", "", "", "", "", "")
	require.NoError(t, err)
	tn2, err := UpsertTenant(ctx, db, 60051, "isouser2", "", "", "", "", "", "")
	require.NoError(t, err)

	require.NoError(t, AddTenantRepos(ctx, db, tn1.ID, []OrgRepo{{Org: "shared-org", Repo: "shared-repo"}}))
	require.NoError(t, AddTenantRepos(ctx, db, tn2.ID, []OrgRepo{{Org: "shared-org", Repo: "shared-repo"}}))

	// Deactivate for tenant 1 only.
	require.NoError(t, DeactivateTenantRepo(ctx, db, tn1.ID, "shared-org", "shared-repo"))

	// Tenant 1 should have no active repos.
	list1, err := ListTenantRepos(ctx, db, tn1.ID)
	require.NoError(t, err)
	assert.Empty(t, list1, "tenant 1's repo should be deactivated")

	// Tenant 2's row must be untouched.
	list2, err := ListTenantRepos(ctx, db, tn2.ID)
	require.NoError(t, err)
	require.Len(t, list2, 1, "tenant 2's repo must remain active")
	assert.True(t, list2[0].Active)
	assert.Equal(t, "shared-repo", list2[0].Repo)
}

func TestDeactivateTenantRepo_ReactivateByAdd(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	tn, err := UpsertTenant(ctx, db, 60007, "reactivateuser", "", "", "", "", "", "")
	require.NoError(t, err)

	require.NoError(t, AddTenantRepos(ctx, db, tn.ID, []OrgRepo{{Org: "org", Repo: "repo"}}))
	require.NoError(t, DeactivateTenantRepo(ctx, db, tn.ID, "org", "repo"))

	list, err := ListTenantRepos(ctx, db, tn.ID)
	require.NoError(t, err)
	require.Len(t, list, 0)

	// Re-adding should reactivate
	require.NoError(t, AddTenantRepos(ctx, db, tn.ID, []OrgRepo{{Org: "org", Repo: "repo"}}))

	list, err = ListTenantRepos(ctx, db, tn.ID)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.True(t, list[0].Active)
}

func TestAddTenantRepos_RepoLimitExceeded(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	tn, err := UpsertTenant(ctx, db, 60008, "limituser", "", "", "", "", "", "")
	require.NoError(t, err)

	// New tenants are auto-enrolled in Pro during the beta preview;
	// read the actual limit.
	got, err := GetTenantByID(ctx, db, tn.ID)
	require.NoError(t, err)
	maxRepos := got.MaxRepos

	// Fill up to limit
	repos := make([]OrgRepo, maxRepos)
	for i := range repos {
		repos[i] = OrgRepo{Org: "org", Repo: "repo" + string(rune('a'+i))}
	}
	require.NoError(t, AddTenantRepos(ctx, db, tn.ID, repos))

	// One more should exceed
	err = AddTenantRepos(ctx, db, tn.ID, []OrgRepo{{Org: "org", Repo: "excess"}})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrRepoLimitExceeded)
}

func TestListTenantRepos_EmptyResult(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	tn, err := UpsertTenant(ctx, db, 60009, "emptyrepouser", "", "", "", "", "", "")
	require.NoError(t, err)

	list, err := ListTenantRepos(ctx, db, tn.ID)
	require.NoError(t, err)
	assert.Empty(t, list)
}

func TestCountTenantRepos(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	tn, err := UpsertTenant(ctx, db, 60010, "countuser", "", "", "", "", "", "")
	require.NoError(t, err)

	// Zero repos initially.
	count, err := CountTenantRepos(ctx, db, tn.ID)
	require.NoError(t, err)
	assert.Equal(t, 0, count)

	// Add repos and verify count.
	require.NoError(t, AddTenantRepos(ctx, db, tn.ID, []OrgRepo{
		{Org: "orgA", Repo: "repo1"},
		{Org: "orgA", Repo: "repo2"},
	}))

	count, err = CountTenantRepos(ctx, db, tn.ID)
	require.NoError(t, err)
	assert.Equal(t, 2, count)
}

func TestGetActiveTenants(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	// Tenant without ToS should not appear
	tn1, err := UpsertTenant(ctx, db, 70001, "notos", "", "", "", "", "", "")
	require.NoError(t, err)
	require.NoError(t, AddTenantRepos(ctx, db, tn1.ID, []OrgRepo{{Org: "org", Repo: "repo"}}))

	// Tenant with ToS and active repo should appear
	tn2, err := UpsertTenant(ctx, db, 70002, "withtos", "", "", "", "", "", "")
	require.NoError(t, err)
	require.NoError(t, AcceptToS(ctx, db, tn2.ID))
	require.NoError(t, AddTenantRepos(ctx, db, tn2.ID, []OrgRepo{{Org: "org", Repo: "repo2"}}))

	tenants, err := GetActiveTenants(ctx, db)
	require.NoError(t, err)

	found := false
	for _, at := range tenants {
		if at.ID == tn2.ID {
			found = true
			assert.Equal(t, "withtos", at.Username)
		}
		assert.NotEqual(t, tn1.ID, at.ID, "tenant without ToS should not appear")
	}
	assert.True(t, found, "tenant with ToS and active repo should appear")
}

func TestGetActiveInstallations(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	tn, err := UpsertTenant(ctx, db, 70003, "activeinstuser", "", "", "", "", "", "")
	require.NoError(t, err)

	require.NoError(t, SaveInstallation(ctx, db, tn.ID, 7001, "Organization", "active", nil, 100))
	require.NoError(t, SaveInstallation(ctx, db, tn.ID, 7002, "Organization", "suspended", nil, 100))
	require.NoError(t, SuspendInstallation(ctx, db, 7002))

	insts, err := GetActiveInstallations(ctx, db, tn.ID, 100)
	require.NoError(t, err)
	require.Len(t, insts, 1)
	assert.Equal(t, int64(7001), insts[0].ID)
	assert.Equal(t, "active", insts[0].Login)
}

func TestAddSampleRepos(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	tn, err := UpsertTenant(ctx, db, 60020, "sampleuser", "", "", "", "", "", "")
	require.NoError(t, err)

	samples := []OrgRepo{
		{Org: "sample-org", Repo: "repo1"},
		{Org: "sample-org", Repo: "repo2"},
	}
	require.NoError(t, AddSampleRepos(ctx, db, tn.ID, samples))

	list, err := ListTenantRepos(ctx, db, tn.ID)
	require.NoError(t, err)
	require.Len(t, list, 2)
	assert.True(t, list[0].Sample)
	assert.True(t, list[1].Sample)
}

func TestSampleReposExcludedFromCount(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	tn, err := UpsertTenant(ctx, db, 60021, "samplecountuser", "", "", "", "", "", "")
	require.NoError(t, err)

	// Add sample repos
	require.NoError(t, AddSampleRepos(ctx, db, tn.ID, []OrgRepo{
		{Org: "sample-org", Repo: "s1"},
		{Org: "sample-org", Repo: "s2"},
	}))

	// Count should be 0 — samples excluded
	count, err := CountTenantRepos(ctx, db, tn.ID)
	require.NoError(t, err)
	assert.Equal(t, 0, count)

	// Add a regular repo
	require.NoError(t, AddTenantRepos(ctx, db, tn.ID, []OrgRepo{{Org: "real-org", Repo: "r1"}}))

	// Count should be 1 — only regular repo
	count, err = CountTenantRepos(ctx, db, tn.ID)
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	// List should have 3 total
	list, err := ListTenantRepos(ctx, db, tn.ID)
	require.NoError(t, err)
	assert.Len(t, list, 3)
}

func TestSampleReposDontBreakPlanLimit(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	tn, err := UpsertTenant(ctx, db, 60022, "samplelimituser", "", "", "", "", "", "")
	require.NoError(t, err)

	got, err := GetTenantByID(ctx, db, tn.ID)
	require.NoError(t, err)
	maxRepos := got.MaxRepos

	// Seed samples — these should not count toward limit
	require.NoError(t, AddSampleRepos(ctx, db, tn.ID, []OrgRepo{
		{Org: "sample", Repo: "s1"},
		{Org: "sample", Repo: "s2"},
	}))

	// Fill to plan limit with regular repos
	repos := make([]OrgRepo, maxRepos)
	for i := range repos {
		repos[i] = OrgRepo{Org: "org", Repo: "repo" + string(rune('a'+i))}
	}
	require.NoError(t, AddTenantRepos(ctx, db, tn.ID, repos))

	// One more regular should exceed
	err = AddTenantRepos(ctx, db, tn.ID, []OrgRepo{{Org: "org", Repo: "excess"}})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrRepoLimitExceeded)
}

func TestMarkRepoAsSample(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	tn, err := UpsertTenant(ctx, db, 60023, "markuser", "", "", "", "", "", "")
	require.NoError(t, err)

	// Add a regular repo
	require.NoError(t, AddTenantRepos(ctx, db, tn.ID, []OrgRepo{{Org: "org", Repo: "repo"}}))

	list, err := ListTenantRepos(ctx, db, tn.ID)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.False(t, list[0].Sample)

	// Mark as sample
	require.NoError(t, MarkRepoAsSample(ctx, db, tn.ID, "org", "repo"))

	list, err = ListTenantRepos(ctx, db, tn.ID)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.True(t, list[0].Sample)

	// Should no longer count
	count, err := CountTenantRepos(ctx, db, tn.ID)
	require.NoError(t, err)
	assert.Equal(t, 0, count)
}

func TestSampleRepoDeactivateAndReactivate(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	tn, err := UpsertTenant(ctx, db, 60024, "samplereactivate", "", "", "", "", "", "")
	require.NoError(t, err)

	// Add sample repo
	require.NoError(t, AddSampleRepos(ctx, db, tn.ID, []OrgRepo{{Org: "org", Repo: "sample-repo"}}))

	// Deactivate
	require.NoError(t, DeactivateTenantRepo(ctx, db, tn.ID, "org", "sample-repo"))
	list, err := ListTenantRepos(ctx, db, tn.ID)
	require.NoError(t, err)
	assert.Empty(t, list)

	// Re-add via regular path — upsert reactivates but preserves sample flag
	require.NoError(t, AddTenantRepos(ctx, db, tn.ID, []OrgRepo{{Org: "org", Repo: "sample-repo"}}))
	list, err = ListTenantRepos(ctx, db, tn.ID)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.True(t, list[0].Sample)
}

func TestAddSampleRepos_NoDataWarning(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	tn, err := UpsertTenant(ctx, db, 60025, "nodatauser", "", "", "", "", "", "")
	require.NoError(t, err)

	// Add sample repos when no repo_meta exists — should succeed (warning logged)
	require.NoError(t, AddSampleRepos(ctx, db, tn.ID, []OrgRepo{
		{Org: "no-data-org", Repo: "no-data-repo"},
	}))

	list, err := ListTenantRepos(ctx, db, tn.ID)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.True(t, list[0].Sample)
}

func TestAddSampleRepos_WithData(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	tn, err := UpsertTenant(ctx, db, 60026, "withdatauser", "", "", "", "", "", "")
	require.NoError(t, err)

	// Insert repo_meta row to simulate previously imported repo
	_, err = db.ExecContext(ctx,
		`INSERT INTO devpulse_repo_meta (org, repo) VALUES ('has-data-org', 'has-data-repo')`)
	require.NoError(t, err)

	// Add sample repo that has data — should succeed (no warning)
	require.NoError(t, AddSampleRepos(ctx, db, tn.ID, []OrgRepo{
		{Org: "has-data-org", Repo: "has-data-repo"},
	}))

	list, err := ListTenantRepos(ctx, db, tn.ID)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.True(t, list[0].Sample)
}

func TestSeedSampleRepos_OnlyWhenNoRepos(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	tn, err := UpsertTenant(ctx, db, 60027, "seedonceuser", "", "", "", "", "", "")
	require.NoError(t, err)

	samples := []OrgRepo{{Org: "seed-org", Repo: "seed-repo"}}

	// Simulate seeding logic: count == 0 → seed
	count, err := CountTenantRepos(ctx, db, tn.ID)
	require.NoError(t, err)
	assert.Equal(t, 0, count)
	require.NoError(t, AddSampleRepos(ctx, db, tn.ID, samples))

	// Now add a regular repo
	require.NoError(t, AddTenantRepos(ctx, db, tn.ID, []OrgRepo{{Org: "real", Repo: "repo"}}))

	// Simulate second login: count > 0 → should not re-seed
	count, err = CountTenantRepos(ctx, db, tn.ID)
	require.NoError(t, err)
	assert.Equal(t, 1, count) // only regular repo counted

	// Verify total is 2 (1 sample + 1 regular)
	list, err := ListTenantRepos(ctx, db, tn.ID)
	require.NoError(t, err)
	assert.Len(t, list, 2)
}

func TestGetActiveReposForInstall(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	tn, err := UpsertTenant(ctx, db, 70004, "activerepouser", "", "", "", "", "", "")
	require.NoError(t, err)

	require.NoError(t, SaveInstallation(ctx, db, tn.ID, 8001, "Organization", "myorg", nil, 100))
	repos := []OrgRepo{
		{Org: "myorg", Repo: "repo1"},
		{Org: "myorg", Repo: "repo2"},
		{Org: "otherorg", Repo: "repo3"},
	}
	require.NoError(t, AddTenantRepos(ctx, db, tn.ID, repos))

	active, err := GetActiveReposForInstall(ctx, db, tn.ID, 8001)
	require.NoError(t, err)
	require.Len(t, active, 2)

	orgRepos := make(map[string]bool)
	for _, r := range active {
		orgRepos[r.Org+"/"+r.Repo] = true
	}
	assert.True(t, orgRepos["myorg/repo1"])
	assert.True(t, orgRepos["myorg/repo2"])
}
