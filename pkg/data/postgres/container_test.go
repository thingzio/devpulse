package postgres

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetContainerActivity_NilDB(t *testing.T) {
	ctx := context.Background()
	s := &Store{db: nil}
	_, err := s.GetContainerActivity(ctx, nil, nil, 180)
	assert.Error(t, err)
}

func TestGetContainerActivity_EmptyDB(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)
	series, err := store.GetContainerActivity(ctx, nil, nil, 180)
	require.NoError(t, err)
	require.NotNil(t, series)
	for _, v := range series.Versions {
		assert.Equal(t, 0, v)
	}
}

func TestGetContainerActivity_WithData(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)

	_, err := store.db.ExecContext(ctx, `INSERT INTO devpulse_container_version(org, repo, package, version_id, tag, created_at)
		VALUES
		('org1', 'repo1', 'pkg1', 1, 'v1.0.0', '2025-01-15T10:00:00Z'),
		('org1', 'repo1', 'pkg1', 2, 'v1.1.0', '2025-01-20T10:00:00Z'),
		('org1', 'repo1', 'pkg1', 3, 'v2.0.0', '2025-02-10T10:00:00Z')`)
	require.NoError(t, err)

	series, err := store.GetContainerActivity(ctx, nil, nil, 730)
	require.NoError(t, err)
	idx := findPeriodIdx(t, series.Labels, "2025-01")
	assert.Equal(t, 2, series.Versions[idx])
	idx2 := findPeriodIdx(t, series.Labels, "2025-02")
	assert.Equal(t, 1, series.Versions[idx2])
}

// TestUpsertContainerVersion_Idempotent guards the container_version
// upsert path against the dup-row class of bug we fixed for events. The
// (org, repo, package, version_id) PK plus the ON CONFLICT DO UPDATE
// clause must collapse repeated imports of the same GitHub-issued
// version_id into a single row, with the latest tag/created_at winning.
// Unlike PR/Issue/Fork events (which keyed on a drifting timestamp),
// version_id is a stable GitHub identifier — but the test makes the
// invariant explicit so future schema changes can't silently regress it.
func TestUpsertContainerVersion_Idempotent(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)

	// First "import" of version_id=42 with tag v1.0.0.
	_, err := store.db.ExecContext(ctx, upsertContainerVersionSQL,
		"org1", "repo1", "pkg1", 42, "v1.0.0", "2025-01-15T10:00:00Z")
	require.NoError(t, err)

	// Second "import" of the SAME version_id with an updated tag (e.g.
	// the registry retagged the underlying image — GitHub would surface
	// the same ID with a new tag value).
	_, err = store.db.ExecContext(ctx, upsertContainerVersionSQL,
		"org1", "repo1", "pkg1", 42, "v1.0.0-fixed", "2025-01-15T10:00:00Z")
	require.NoError(t, err)

	var rows int
	require.NoError(t, store.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM devpulse_container_version WHERE org='org1' AND repo='repo1' AND package='pkg1' AND version_id=42`,
	).Scan(&rows))
	assert.Equal(t, 1, rows, "re-importing the same version_id must converge to a single row")

	var tag string
	require.NoError(t, store.db.QueryRowContext(ctx,
		`SELECT tag FROM devpulse_container_version WHERE org='org1' AND repo='repo1' AND package='pkg1' AND version_id=42`,
	).Scan(&tag))
	assert.Equal(t, "v1.0.0-fixed", tag, "tag must reflect the latest import")
}

func TestGetContainerActivity_FilterByOrg(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)

	_, err := store.db.ExecContext(ctx, `INSERT INTO devpulse_container_version(org, repo, package, version_id, tag, created_at)
		VALUES
		('org1', 'repo1', 'pkg1', 1, 'v1.0.0', '2025-01-15T10:00:00Z'),
		('org2', 'repo2', 'pkg2', 2, 'v1.0.0', '2025-01-20T10:00:00Z')`)
	require.NoError(t, err)

	org := "org1"
	series, err := store.GetContainerActivity(ctx, &org, nil, 730)
	require.NoError(t, err)
	idx := findPeriodIdx(t, series.Labels, "2025-01")
	assert.Equal(t, 1, series.Versions[idx])
}
