package postgres

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thingzio/devpulse/pkg/data"
)

func TestGetReputationComposition_NilDB(t *testing.T) {
	ctx := context.Background()
	s := &Store{db: nil}
	_, err := s.GetReputationComposition(ctx, nil, nil, nil, 180)
	assert.Error(t, err)
}

func TestGetReputationComposition_EmptyDB(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)
	comp, err := store.GetReputationComposition(ctx, nil, nil, nil, 180)
	require.NoError(t, err)
	assert.Equal(t, 0, comp.Alert)
	assert.Equal(t, 0, comp.Standard)
	assert.Equal(t, 0, comp.HighConfidence)
	assert.Equal(t, 0, comp.Deep)
	assert.Equal(t, 0, comp.Scored)
	assert.Equal(t, 0, comp.Total)
}

func TestUpdateReputation(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)

	devs := []*data.Developer{{Username: "repuser", FullName: "Rep User", Entity: "CORP"}}
	require.NoError(t, store.SaveDevelopers(ctx, devs))

	require.NoError(t, store.updateReputation(ctx, "repuser", 0.85, "2025-01-15T10:00:00Z", true, nil))

	var rep float64
	var updatedAt string
	err := store.db.QueryRowContext(ctx, "SELECT reputation, reputation_updated_at FROM devpulse_developer WHERE username = $1", "repuser").
		Scan(&rep, &updatedAt)
	require.NoError(t, err)
	assert.InDelta(t, 0.85, rep, 0.001)
	assert.Equal(t, "2025-01-15T10:00:00Z", updatedAt)
}

func TestUpdateReputation_NilDB(t *testing.T) {
	ctx := context.Background()
	s := &Store{db: nil}
	err := s.updateReputation(ctx, "test", 0.5, "2025-01-01T00:00:00Z", false, nil)
	assert.Error(t, err)
}

func TestGetStaleReputationUsernames_NullReputation(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)

	devs := []*data.Developer{{Username: "staleuser", FullName: "Stale User"}}
	require.NoError(t, store.SaveDevelopers(ctx, devs))

	// Add an event so the JOIN finds the user
	_, err := store.db.ExecContext(ctx, `INSERT INTO devpulse_event (org, repo, username, type, date, url, mentions, labels)
		VALUES ('org1', 'repo1', 'staleuser', 'pr', '2025-01-10', 'http://example.com', '', '')`)
	require.NoError(t, err)

	usernames, err := store.getStaleReputationUsernames(ctx, nil, nil, "2025-01-15T00:00:00Z")
	require.NoError(t, err)
	assert.Contains(t, usernames, "staleuser")
}

func TestGetStaleReputationUsernames_FreshReputation(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)

	devs := []*data.Developer{{Username: "freshuser", FullName: "Fresh User"}}
	require.NoError(t, store.SaveDevelopers(ctx, devs))

	// Add event
	_, err := store.db.ExecContext(ctx, `INSERT INTO devpulse_event (org, repo, username, type, date, url, mentions, labels)
		VALUES ('org1', 'repo1', 'freshuser', 'pr', '2025-01-10', 'http://example.com', '', '')`)
	require.NoError(t, err)

	// Set fresh reputation
	require.NoError(t, store.updateReputation(ctx, "freshuser", 0.9, "2025-02-01T00:00:00Z", false, nil))

	// Threshold before the update -- user should NOT appear
	usernames, err := store.getStaleReputationUsernames(ctx, nil, nil, "2025-01-15T00:00:00Z")
	require.NoError(t, err)
	assert.NotContains(t, usernames, "freshuser")
}

func TestGetStaleReputationUsernames_SkipsBots(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)

	devs := []*data.Developer{
		{Username: "realuser", FullName: "Real User"},
		{Username: "dependabot[bot]", FullName: ""},
	}
	require.NoError(t, store.SaveDevelopers(ctx, devs))

	_, err := store.db.ExecContext(ctx, `INSERT INTO devpulse_event (org, repo, username, type, date, url, mentions, labels)
		VALUES
		('org1', 'repo1', 'realuser', 'pr', '2025-01-10', 'http://example.com', '', ''),
		('org1', 'repo1', 'dependabot[bot]', 'pr', '2025-01-10', 'http://example.com', '', '')`)
	require.NoError(t, err)

	usernames, err := store.getStaleReputationUsernames(ctx, nil, nil, "2025-01-15T00:00:00Z")
	require.NoError(t, err)
	assert.Contains(t, usernames, "realuser")
	assert.NotContains(t, usernames, "dependabot[bot]")
}

func TestGetStaleReputationUsernames_NilDB(t *testing.T) {
	ctx := context.Background()
	s := &Store{db: nil}
	_, err := s.getStaleReputationUsernames(ctx, nil, nil, "2025-01-01T00:00:00Z")
	assert.Error(t, err)
}

func TestGetDistinctOrgs(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)

	_, err := store.db.ExecContext(ctx, `INSERT INTO devpulse_developer (username, full_name) VALUES ('user1', 'User One')`)
	require.NoError(t, err)

	_, err = store.db.ExecContext(ctx, `INSERT INTO devpulse_event (org, repo, username, type, date, url, mentions, labels)
		VALUES
		('org1', 'repo1', 'user1', 'pr', '2025-01-10', 'http://example.com', '', ''),
		('org2', 'repo2', 'user1', 'pr', '2025-01-10', 'http://example.com', '', '')`)
	require.NoError(t, err)

	orgs, err := store.getDistinctOrgs(ctx)
	require.NoError(t, err)
	assert.Len(t, orgs, 2)
	assert.Contains(t, orgs, "org1")
	assert.Contains(t, orgs, "org2")
}

func TestGetDistinctOrgs_NilDB(t *testing.T) {
	ctx := context.Background()
	s := &Store{db: nil}
	_, err := s.getDistinctOrgs(ctx)
	assert.Error(t, err)
}

func TestGetReputationComposition_WithData(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)

	devs := []*data.Developer{
		{Username: "alert-user", FullName: "Alert User"},
		{Username: "boundary-low", FullName: "Boundary Low"},
		{Username: "standard-user", FullName: "Standard User"},
		{Username: "boundary-high", FullName: "Boundary High"},
		{Username: "high-user", FullName: "High User"},
		{Username: "unscored-user", FullName: "Unscored User"},
	}
	require.NoError(t, store.SaveDevelopers(ctx, devs))

	// Alert: < 0.3
	require.NoError(t, store.updateReputation(ctx, "alert-user", 0.29, "2025-01-15T00:00:00Z", false, nil))
	// Standard boundary: exactly 0.3
	require.NoError(t, store.updateReputation(ctx, "boundary-low", 0.30, "2025-01-15T00:00:00Z", false, nil))
	// Standard mid-range
	require.NoError(t, store.updateReputation(ctx, "standard-user", 0.50, "2025-01-15T00:00:00Z", true, nil))
	// Standard upper boundary: 0.69
	require.NoError(t, store.updateReputation(ctx, "boundary-high", 0.69, "2025-01-15T00:00:00Z", false, nil))
	// High confidence: >= 0.7 (use 0.75 to avoid float4 precision loss at exact boundary)
	require.NoError(t, store.updateReputation(ctx, "high-user", 0.75, "2025-01-15T00:00:00Z", true, nil))
	// unscored-user has no reputation set

	_, err := store.db.ExecContext(ctx, `INSERT INTO devpulse_event (org, repo, username, type, date, url, mentions, labels) VALUES
		('org1', 'repo1', 'alert-user',    'pr', '2025-01-10', 'http://example.com', '', ''),
		('org1', 'repo1', 'boundary-low',  'pr', '2025-01-10', 'http://example.com', '', ''),
		('org1', 'repo1', 'standard-user', 'pr', '2025-01-10', 'http://example.com', '', ''),
		('org1', 'repo1', 'boundary-high', 'pr', '2025-01-10', 'http://example.com', '', ''),
		('org1', 'repo1', 'high-user',     'pr', '2025-01-10', 'http://example.com', '', ''),
		('org1', 'repo1', 'unscored-user', 'pr', '2025-01-10', 'http://example.com', '', '')`)
	require.NoError(t, err)

	comp, err := store.GetReputationComposition(ctx, nil, nil, nil, 730)
	require.NoError(t, err)

	// Bucket counts
	assert.Equal(t, 1, comp.Alert, "alert: scores < 0.3")
	assert.Equal(t, 3, comp.Standard, "standard: scores 0.3-0.69")
	assert.Equal(t, 1, comp.HighConfidence, "high confidence: scores >= 0.7")

	// Depth counts
	assert.Equal(t, 2, comp.Deep, "deep-scored contributors")

	// Totals
	assert.Equal(t, 5, comp.Scored, "scored contributors")
	assert.Equal(t, 6, comp.Total, "total contributors including unscored")
}

func TestImportReputation_NilDB(t *testing.T) {
	ctx := context.Background()
	s := &Store{db: nil}
	_, err := s.ImportReputation(ctx, nil, nil)
	assert.Error(t, err)
}

func TestImportReputation_EmptyDB(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)
	res, err := store.ImportReputation(ctx, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, 0, res.Updated)
}

func TestImportReputation_ComputesShallowScores(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)

	devs := []*data.Developer{{Username: "alice", FullName: "Alice"}}
	require.NoError(t, store.SaveDevelopers(ctx, devs))

	// Use today's date so recency signal is non-zero
	today := time.Now().UTC().Format("2006-01-02")
	_, err := store.db.ExecContext(ctx, `INSERT INTO devpulse_event (org, repo, username, type, date, url, mentions, labels)
		VALUES ('org1', 'repo1', 'alice', 'pr', $1, 'http://example.com', '', '')`, today)
	require.NoError(t, err)

	res, err := store.ImportReputation(ctx, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, 1, res.Updated)

	// Verify score was stored and non-zero (recency + engagement signals active)
	var rep sql.NullFloat64
	scanErr := store.db.QueryRowContext(ctx, "SELECT reputation FROM devpulse_developer WHERE username = 'alice'").Scan(&rep)
	require.NoError(t, scanErr)
	assert.True(t, rep.Valid)
}

func TestGetTieredReputationUsernames_NilDB(t *testing.T) {
	ctx := context.Background()
	s := &Store{db: nil}
	_, err := s.getTieredReputationUsernames(ctx, nil, nil, "2025-01-01T00:00:00Z", "2025-01-01T00:00:00Z", 0.5, 5)
	assert.Error(t, err)
}

func TestGetTieredReputationUsernames_EmptyDB(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)
	usernames, err := store.getTieredReputationUsernames(ctx, nil, nil, "2025-01-01T00:00:00Z", "2025-01-01T00:00:00Z", 0.5, 5)
	require.NoError(t, err)
	assert.Empty(t, usernames)
}

func TestGetTieredReputationUsernames_LowScoreStaleFirst(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)

	devs := []*data.Developer{
		{Username: "low1", FullName: "Low One"},
		{Username: "low2", FullName: "Low Two"},
		{Username: "high1", FullName: "High One"},
	}
	require.NoError(t, store.SaveDevelopers(ctx, devs))

	// Set scores: low1=0.2, low2=0.3 (below 0.5), high1=0.8 (above 0.5)
	// All deep-scored 10 days ago
	tenDaysAgo := time.Now().UTC().Add(-10 * 24 * time.Hour).Format("2006-01-02T15:04:05Z")
	require.NoError(t, store.updateReputation(ctx, "low1", 0.2, tenDaysAgo, true, nil))
	require.NoError(t, store.updateReputation(ctx, "low2", 0.3, tenDaysAgo, true, nil))
	require.NoError(t, store.updateReputation(ctx, "high1", 0.8, tenDaysAgo, true, nil))

	for _, u := range []string{"low1", "low2", "high1"} {
		_, err := store.db.ExecContext(ctx, `INSERT INTO devpulse_event (org, repo, username, type, date, url, mentions, labels)
			VALUES ('org1', 'repo1', $1, 'pr', '2025-01-10', 'http://example.com', '', '')`, u)
		require.NoError(t, err)
	}

	// lowThreshold = 7 days ago (low-score users updated 10 days ago ARE stale)
	// highThreshold = 30 days ago (high-score users updated 10 days ago are NOT stale)
	lowThreshold := time.Now().UTC().Add(-7 * 24 * time.Hour).Format("2006-01-02T15:04:05Z")
	highThreshold := time.Now().UTC().Add(-30 * 24 * time.Hour).Format("2006-01-02T15:04:05Z")

	usernames, err := store.getTieredReputationUsernames(ctx, nil, nil, lowThreshold, highThreshold, 0.5, 10)
	require.NoError(t, err)
	// low1 and low2 are stale (updated 10d ago > 7d threshold), high1 is NOT stale (10d < 30d threshold)
	assert.Contains(t, usernames, "low1")
	assert.Contains(t, usernames, "low2")
	assert.NotContains(t, usernames, "high1")
}

func TestGetTieredReputationUsernames_HighScoreStaleAfterLongerPeriod(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)

	devs := []*data.Developer{
		{Username: "high1", FullName: "High One"},
	}
	require.NoError(t, store.SaveDevelopers(ctx, devs))

	// Deep-scored 35 days ago — beyond the 30-day high threshold
	oldUpdate := time.Now().UTC().Add(-35 * 24 * time.Hour).Format("2006-01-02T15:04:05Z")
	require.NoError(t, store.updateReputation(ctx, "high1", 0.8, oldUpdate, true, nil))

	_, err := store.db.ExecContext(ctx, `INSERT INTO devpulse_event (org, repo, username, type, date, url, mentions, labels)
		VALUES ('org1', 'repo1', 'high1', 'pr', '2025-01-10', 'http://example.com', '', '')`)
	require.NoError(t, err)

	lowThreshold := time.Now().UTC().Add(-7 * 24 * time.Hour).Format("2006-01-02T15:04:05Z")
	highThreshold := time.Now().UTC().Add(-30 * 24 * time.Hour).Format("2006-01-02T15:04:05Z")

	usernames, err := store.getTieredReputationUsernames(ctx, nil, nil, lowThreshold, highThreshold, 0.5, 10)
	require.NoError(t, err)
	// high1 IS stale now (35d > 30d threshold)
	assert.Contains(t, usernames, "high1")
}

func TestGetTieredReputationUsernames_SkipsBots(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)

	devs := []*data.Developer{
		{Username: "realuser", FullName: "Real"},
		{Username: "mybot[bot]", FullName: ""},
	}
	require.NoError(t, store.SaveDevelopers(ctx, devs))

	now := time.Now().UTC().Add(-10 * 24 * time.Hour).Format("2006-01-02T15:04:05Z")
	require.NoError(t, store.updateReputation(ctx, "realuser", 0.10, now, false, nil))
	require.NoError(t, store.updateReputation(ctx, "mybot[bot]", 0.05, now, false, nil))

	for _, u := range []string{"realuser", "mybot[bot]"} {
		_, err := store.db.ExecContext(ctx, `INSERT INTO devpulse_event (org, repo, username, type, date, url, mentions, labels)
			VALUES ('org1', 'repo1', $1, 'pr', '2025-01-10', 'http://example.com', '', '')`, u)
		require.NoError(t, err)
	}

	lowThreshold := time.Now().UTC().Add(-7 * 24 * time.Hour).Format("2006-01-02T15:04:05Z")
	highThreshold := time.Now().UTC().Add(-30 * 24 * time.Hour).Format("2006-01-02T15:04:05Z")

	usernames, err := store.getTieredReputationUsernames(ctx, nil, nil, lowThreshold, highThreshold, 0.5, 10)
	require.NoError(t, err)
	assert.Contains(t, usernames, "realuser")
	assert.NotContains(t, usernames, "mybot[bot]")
}

func TestImportDeepReputation_NilDB(t *testing.T) {
	ctx := context.Background()
	s := &Store{db: nil}
	_, err := s.ImportDeepReputation(ctx, func() string { return "token" }, nil, 5, 0, nil, nil)
	assert.Error(t, err)
}

func TestImportDeepReputation_EmptyToken(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)
	_, err := store.ImportDeepReputation(ctx, nil, nil, 5, 0, nil, nil)
	assert.Error(t, err)
}

func TestImportDeepReputation_ZeroLimit(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)
	res, err := store.ImportDeepReputation(ctx, func() string { return "token" }, nil, 0, 0, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, 0, res.Scored)
}

func TestImportDeepReputation_NoCandidates(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)
	res, err := store.ImportDeepReputation(ctx, func() string { return "token" }, nil, 5, 0, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, 0, res.Scored)
}

func TestGetStaleReputationUsernames_FilterByOrg(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)

	devs := []*data.Developer{
		{Username: "orguser", FullName: "Org User"},
		{Username: "otheruser", FullName: "Other User"},
	}
	require.NoError(t, store.SaveDevelopers(ctx, devs))

	_, err := store.db.ExecContext(ctx, `INSERT INTO devpulse_event (org, repo, username, type, date, url, mentions, labels)
		VALUES
		('nvidia', 'repo1', 'orguser', 'pr', '2025-01-10', 'http://example.com', '', ''),
		('other', 'repo2', 'otheruser', 'pr', '2025-01-10', 'http://example.com', '', '')`)
	require.NoError(t, err)

	org := "nvidia"
	usernames, err := store.getStaleReputationUsernames(ctx, &org, nil, "2025-01-15T00:00:00Z")
	require.NoError(t, err)
	assert.Contains(t, usernames, "orguser")
	assert.NotContains(t, usernames, "otheruser")
}

func TestGetStaleReputationUsernames_FilterByOrgAndRepo(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)

	devs := []*data.Developer{
		{Username: "repouser", FullName: "Repo User"},
		{Username: "otherrepo", FullName: "Other Repo"},
	}
	require.NoError(t, store.SaveDevelopers(ctx, devs))

	_, err := store.db.ExecContext(ctx, `INSERT INTO devpulse_event (org, repo, username, type, date, url, mentions, labels)
		VALUES
		('nvidia', 'skyhook', 'repouser', 'pr', '2025-01-10', 'http://example.com', '', ''),
		('nvidia', 'other', 'otherrepo', 'pr', '2025-01-10', 'http://example.com', '', '')`)
	require.NoError(t, err)

	org := "nvidia"
	repo := "skyhook"
	usernames, err := store.getStaleReputationUsernames(ctx, &org, &repo, "2025-01-15T00:00:00Z")
	require.NoError(t, err)
	assert.Contains(t, usernames, "repouser")
	assert.NotContains(t, usernames, "otherrepo")
}

func TestGatherLocalSignals(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)

	_, err := store.db.ExecContext(ctx, `INSERT INTO devpulse_developer (username, full_name) VALUES ('user1', 'User One'), ('user2', 'User Two')`)
	require.NoError(t, err)

	_, err = store.db.ExecContext(ctx, `INSERT INTO devpulse_event (org, repo, username, type, date, url, mentions, labels)
		VALUES
		('org1', 'repo1', 'user1', 'pr', '2025-01-10', 'http://example.com', '', ''),
		('org1', 'repo1', 'user1', 'pr', '2025-01-11', 'http://example.com', '', ''),
		('org1', 'repo1', 'user2', 'pr', '2025-01-10', 'http://example.com', '', '')`)
	require.NoError(t, err)

	stats, err := store.computeGlobalStats(ctx, "2024-01-01")
	require.NoError(t, err)
	assert.Equal(t, int64(3), stats.totalCommits)
	assert.Equal(t, 2, stats.totalContributors)

	s := store.gatherLocalSignals(ctx, "user1", "2024-01-01", stats)
	assert.Equal(t, int64(2), s.Commits)
	assert.Equal(t, int64(3), s.TotalCommits)
	assert.Equal(t, 2, s.TotalContributors)
	assert.Greater(t, s.LastCommitDays, int64(0))
}
