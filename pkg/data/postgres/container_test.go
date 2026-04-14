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
