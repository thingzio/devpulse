package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetMaxEventTime_NoEvents(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()

	got, err := store.GetMaxEventTime(ctx, "testorg", "testrepo")
	require.NoError(t, err)
	assert.True(t, got.IsZero(), "expected zero time for repo with no events")
}

func TestGetMaxEventTime_WithEvents(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()

	// Insert developer (required by FK).
	_, err := store.db.ExecContext(ctx,
		`INSERT INTO devpulse_developer (username, full_name) VALUES ('user1', 'User One')`)
	require.NoError(t, err)

	// Insert test events with known created_at timestamps. created_at drives
	// the result; date is bound separately because the two columns are now
	// distinct types (DATE vs TIMESTAMPTZ) and only needs to vary to satisfy
	// the (org, repo, username, type, date) primary key.
	for _, ts := range []string{"2025-01-15T08:00:00Z", "2025-03-20T21:14:12Z", "2025-02-10T12:30:00Z"} {
		_, insertErr := store.db.ExecContext(ctx,
			`INSERT INTO devpulse_event (org, repo, username, type, date, url, mentions, labels, created_at)
			 VALUES ($1, $2, 'user1', 'pr', $3, '', '', '', $4)`,
			"testorg", "testrepo", ts[:10], ts)
		require.NoError(t, insertErr)
	}

	got, err := store.GetMaxEventTime(ctx, "testorg", "testrepo")
	require.NoError(t, err)
	// Full instant, not just the day — the time component must survive the
	// TIMESTAMPTZ round-trip.
	assert.Equal(t, "2025-03-20T21:14:12Z", got.Format(time.RFC3339))
}

func TestGetMaxEventTime_NilDB(t *testing.T) {
	store := &Store{}
	ctx := context.Background()

	_, err := store.GetMaxEventTime(ctx, "org", "repo")
	assert.Error(t, err)
}

func TestHasForkEvents(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()

	_, err := store.db.ExecContext(ctx,
		`INSERT INTO devpulse_developer (username, full_name) VALUES ('alice', 'Alice')`)
	require.NoError(t, err)

	// Empty: no fork rows.
	got, err := store.HasForkEvents(ctx, "org1", "repo1")
	require.NoError(t, err)
	assert.False(t, got, "no fork rows: HasForkEvents must return false")

	// Add a non-fork event — still no forks.
	_, err = store.db.ExecContext(ctx,
		`INSERT INTO devpulse_event(org, repo, username, type, date, url, mentions, labels, number, created_at)
		 VALUES ('org1', 'repo1', 'alice', 'pr', '2026-04-01', 'http://x', '', '', 1, '2026-04-01T00:00:00Z')`)
	require.NoError(t, err)
	got, err = store.HasForkEvents(ctx, "org1", "repo1")
	require.NoError(t, err)
	assert.False(t, got, "PR rows alone must not count as fork data")

	// Add a fork.
	_, err = store.db.ExecContext(ctx,
		`INSERT INTO devpulse_event(org, repo, username, type, date, url, mentions, labels, created_at)
		 VALUES ('org1', 'repo1', 'alice', 'fork', '2026-04-02', 'http://f', '', '', '2026-04-02T00:00:00Z')`)
	require.NoError(t, err)
	got, err = store.HasForkEvents(ctx, "org1", "repo1")
	require.NoError(t, err)
	assert.True(t, got, "fork row present: HasForkEvents must return true")

	// Different repo with no forks should still return false.
	got, err = store.HasForkEvents(ctx, "org1", "other-repo")
	require.NoError(t, err)
	assert.False(t, got)
}

func TestHasForkEvents_NilDB(t *testing.T) {
	store := &Store{}
	_, err := store.HasForkEvents(context.Background(), "org", "repo")
	assert.Error(t, err)
}
