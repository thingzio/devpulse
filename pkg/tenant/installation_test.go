package tenant

import (
	"context"
	"encoding/json"
	"testing"

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

	// Default free plan has max_repos=3 (from DB default after migration).
	// Read actual limit.
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
