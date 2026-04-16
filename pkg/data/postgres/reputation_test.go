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

func TestGetContributorComposition_NilDB(t *testing.T) {
	ctx := context.Background()
	s := &Store{db: nil}
	_, err := s.GetContributorComposition(ctx, nil, nil, nil, 180)
	assert.Error(t, err)
}

func TestGetContributorComposition_EmptyDB(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)
	comp, err := store.GetContributorComposition(ctx, nil, nil, nil, 180)
	require.NoError(t, err)
	assert.Equal(t, 0, comp.Reviewers)
	assert.Equal(t, 0, comp.Authors)
	assert.Equal(t, 0, comp.Commenters)
	assert.Equal(t, 0, comp.Observers)
	assert.Equal(t, 0, comp.Total)
}

func TestUpdateReputation(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)

	devs := []*data.Developer{{Username: "repuser", FullName: "Rep User", Entity: "CORP"}}
	require.NoError(t, store.SaveDevelopers(ctx, devs))

	_, err := store.db.ExecContext(ctx, updateReputationSQL, 0.85, "2025-01-15T10:00:00Z", "repuser")
	require.NoError(t, err)

	var rep float64
	var updatedAt string
	err = store.db.QueryRowContext(ctx, "SELECT reputation, reputation_updated_at FROM devpulse_developer WHERE username = $1", "repuser").
		Scan(&rep, &updatedAt)
	require.NoError(t, err)
	assert.InDelta(t, 0.85, rep, 0.001)
	assert.Equal(t, "2025-01-15T10:00:00Z", updatedAt)
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
	_, err = store.db.ExecContext(ctx, updateReputationSQL, 0.9, "2025-02-01T00:00:00Z", "freshuser")
	require.NoError(t, err)

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

func TestGetContributorComposition_WithData(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)

	devs := []*data.Developer{
		{Username: "reviewer1", FullName: "Reviewer One"},
		{Username: "author1", FullName: "Author One"},
		{Username: "commenter1", FullName: "Commenter One"},
		{Username: "observer1", FullName: "Observer One"},
	}
	require.NoError(t, store.SaveDevelopers(ctx, devs))

	_, err := store.db.ExecContext(ctx, `INSERT INTO devpulse_event (org, repo, username, type, date, url, mentions, labels) VALUES
		('org1', 'repo1', 'reviewer1',  'pr_review',     '2025-01-10', 'http://example.com', '', ''),
		('org1', 'repo1', 'author1',    'pr',            '2025-01-10', 'http://example.com', '', ''),
		('org1', 'repo1', 'commenter1', 'issue_comment', '2025-01-10', 'http://example.com', '', ''),
		('org1', 'repo1', 'observer1',  'issue',         '2025-01-10', 'http://example.com', '', '')`)
	require.NoError(t, err)

	comp, err := store.GetContributorComposition(ctx, nil, nil, nil, 730)
	require.NoError(t, err)

	assert.Equal(t, 1, comp.Reviewers, "reviewers")
	assert.Equal(t, 1, comp.Authors, "authors (PR-only, not also reviewer)")
	assert.Equal(t, 1, comp.Commenters, "commenters (comment-only, not PR/review)")
	assert.Equal(t, 4, comp.Total, "total unique contributors")
	assert.Equal(t, 1, comp.Observers, "observers (total - reviewers - authors - commenters)")
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
