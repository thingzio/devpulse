package tenant

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestListTenantSummaries(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	// User without profile fields (pre-existing / invited).
	_, err := UpsertTenant(ctx, db, 80001, "summaryuser1", "s1@test.com", "", "", "", "", "")
	require.NoError(t, err)
	// User with profile fields (re-authenticated).
	_, err = UpsertTenant(ctx, db, 80002, "summaryuser2", "s2@test.com", "", "Some User", "Acme", "NYC", "Dev")
	require.NoError(t, err)

	summaries, err := ListTenantSummaries(ctx, db)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(summaries), 2)

	byUsername := make(map[string]TenantSummary)
	for _, s := range summaries {
		byUsername[s.Username] = s
	}
	// Missing profile: name empty, email present.
	s1 := byUsername["summaryuser1"]
	assert.Equal(t, "s1@test.com", s1.Email)
	assert.Empty(t, s1.Name)

	// Populated profile: name present.
	s2 := byUsername["summaryuser2"]
	assert.Equal(t, "Some User", s2.Name)
	assert.Equal(t, "s2@test.com", s2.Email)
}

func TestListTenantSummaries_WithSession(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	tn, err := UpsertTenant(ctx, db, 80003, "sessionsummary", "", "", "", "", "", "")
	require.NoError(t, err)

	_, err = CreateSession(ctx, db, tn.ID, 7*24*60*60*1e9) // 7 days
	require.NoError(t, err)

	summaries, err := ListTenantSummaries(ctx, db)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(summaries), 1)

	for _, s := range summaries {
		if s.Username == "sessionsummary" {
			require.NotNil(t, s.LastSignIn)
			return
		}
	}
	t.Fatal("tenant with session not found in summaries")
}

func TestGetTenantDetailByUsername(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	// Create without profile fields first.
	tn, err := UpsertTenant(ctx, db, 80004, "detailuser", "detail@test.com", "https://avatar.test", "", "", "", "")
	require.NoError(t, err)
	require.NoError(t, UpdatePlan(ctx, db, tn.ID, "pro", 25, 15000))

	detail, err := GetTenantDetailByUsername(ctx, db, "detailuser")
	require.NoError(t, err)
	assert.Equal(t, tn.ID, detail.ID)
	assert.Equal(t, "detailuser", detail.Username)
	assert.Equal(t, "detail@test.com", detail.Email)
	assert.Equal(t, "pro", detail.Plan)
	assert.Equal(t, 25, detail.MaxRepos)
	assert.Equal(t, 15000, detail.MaxEventsPerWeek)
	// Profile fields empty before re-auth.
	assert.Empty(t, detail.Name)
	assert.Empty(t, detail.Company)
	assert.Empty(t, detail.Location)
	assert.Empty(t, detail.Bio)

	// Re-authenticate with profile fields.
	_, err = UpsertTenant(ctx, db, 80004, "detailuser", "detail@test.com", "https://avatar.test",
		"Detail User", "Acme Corp", "London", "Engineer")
	require.NoError(t, err)

	detail2, err := GetTenantDetailByUsername(ctx, db, "detailuser")
	require.NoError(t, err)
	assert.Equal(t, "Detail User", detail2.Name)
	assert.Equal(t, "Acme Corp", detail2.Company)
	assert.Equal(t, "London", detail2.Location)
	assert.Equal(t, "Engineer", detail2.Bio)
}

func TestGetTenantDetailByUsername_NotFound(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	_, err := GetTenantDetailByUsername(ctx, db, "nonexistent")
	require.Error(t, err)
}

func TestGetTenantIDByUsername(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	tn, err := UpsertTenant(ctx, db, 80005, "idbyname", "", "", "", "", "", "")
	require.NoError(t, err)

	id, err := GetTenantIDByUsername(ctx, db, "idbyname")
	require.NoError(t, err)
	assert.Equal(t, tn.ID, id)
}

func TestGetTenantIDByUsername_NotFound(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	_, err := GetTenantIDByUsername(ctx, db, "ghost")
	require.Error(t, err)
}

func TestInsertMinimalTenant(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	id, err := InsertMinimalTenant(ctx, db, 80006, "minimaluser")
	require.NoError(t, err)
	require.NotEmpty(t, id)

	// Verify tenant exists with defaults
	got, err := GetTenantByID(ctx, db, id)
	require.NoError(t, err)
	assert.Equal(t, "minimaluser", got.Username)
	assert.Equal(t, int64(80006), got.GitHubID)
	assert.Equal(t, "free", got.Plan)
}

func TestInsertMinimalTenant_Upsert(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	id1, err := InsertMinimalTenant(ctx, db, 80007, "mindup")
	require.NoError(t, err)

	// Same github_id should return same ID
	id2, err := InsertMinimalTenant(ctx, db, 80007, "mindup")
	require.NoError(t, err)
	assert.Equal(t, id1, id2)
}

func TestClearUpgradeRequest(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	tn, err := UpsertTenant(ctx, db, 80008, "upgradeuser", "", "", "", "", "", "")
	require.NoError(t, err)

	// Request upgrade first
	req, err := RequestUpgrade(ctx, db, tn.ID)
	require.NoError(t, err)
	require.NotNil(t, req)

	// Verify upgrade_requested_at is set
	got, err := GetTenantByID(ctx, db, tn.ID)
	require.NoError(t, err)
	require.NotNil(t, got.UpgradeRequestedAt)

	// Clear it
	require.NoError(t, ClearUpgradeRequest(ctx, db, tn.ID))

	got, err = GetTenantByID(ctx, db, tn.ID)
	require.NoError(t, err)
	assert.Nil(t, got.UpgradeRequestedAt)
}

func TestRequestUpgrade(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	tn, err := UpsertTenant(ctx, db, 80009, "requpgrade", "u@test.com", "", "", "", "", "")
	require.NoError(t, err)

	// First request returns details
	req, err := RequestUpgrade(ctx, db, tn.ID)
	require.NoError(t, err)
	require.NotNil(t, req)
	assert.Equal(t, "requpgrade", req.Username)
	assert.Equal(t, "free", req.Plan)

	// Second request is idempotent — returns nil
	req2, err := RequestUpgrade(ctx, db, tn.ID)
	require.NoError(t, err)
	assert.Nil(t, req2)
}

func TestValidateSession_InvalidToken(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	_, err := ValidateSession(ctx, db, "totally-invalid-token")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrSessionInvalid)
}
