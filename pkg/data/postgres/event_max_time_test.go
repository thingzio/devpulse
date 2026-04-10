package postgres

import (
	"context"
	"testing"

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
		`INSERT INTO developer (username, full_name) VALUES ('user1', 'User One')`)
	require.NoError(t, err)

	// Insert test events with known created_at timestamps.
	// PK is (org, repo, username, type, date) — vary date for uniqueness.
	for _, ts := range []string{"2025-01-15", "2025-03-20", "2025-02-10"} {
		_, insertErr := store.db.ExecContext(ctx,
			`INSERT INTO event (org, repo, username, type, date, url, mentions, labels, created_at)
			 VALUES ($1, $2, 'user1', 'pr', $3, '', '', '', $3)`,
			"testorg", "testrepo", ts)
		require.NoError(t, insertErr)
	}

	got, err := store.GetMaxEventTime(ctx, "testorg", "testrepo")
	require.NoError(t, err)
	assert.Equal(t, "2025-03-20", got.Format("2006-01-02"))
}

func TestGetMaxEventTime_NilDB(t *testing.T) {
	store := &Store{}
	ctx := context.Background()

	_, err := store.GetMaxEventTime(ctx, "org", "repo")
	assert.Error(t, err)
}
