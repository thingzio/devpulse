package postgres

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetAllOrgRepos(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)
	seedTestData(t, store)
	repos, err := store.GetAllOrgRepos(ctx)
	require.NoError(t, err)
	assert.NotEmpty(t, repos)
}

func TestGetAllOrgRepos_NilDB(t *testing.T) {
	ctx := context.Background()
	s := &Store{db: nil}
	_, err := s.GetAllOrgRepos(ctx)
	assert.Error(t, err)
}

func TestGetOrgLike(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)
	seedTestData(t, store)
	items, err := store.GetOrgLike(ctx, "test", 10)
	require.NoError(t, err)
	assert.NotEmpty(t, items)
}

func TestGetOrgLike_EmptyQuery(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)
	_, err := store.GetOrgLike(ctx, "", 10)
	assert.Error(t, err)
}

func TestGetOrgLike_NilDB(t *testing.T) {
	ctx := context.Background()
	s := &Store{db: nil}
	_, err := s.GetOrgLike(ctx, "test", 10)
	assert.Error(t, err)
}

func TestGetAllOrgRepos_EmptyDB(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)
	repos, err := store.GetAllOrgRepos(ctx)
	require.NoError(t, err)
	assert.Empty(t, repos)
}

func TestGetDeveloperPercentages_NilDB(t *testing.T) {
	ctx := context.Background()
	s := &Store{db: nil}
	_, err := s.GetDeveloperPercentages(ctx, nil, nil, nil, nil, 6)
	assert.Error(t, err)
}

func TestGetEntityPercentages_NilDB(t *testing.T) {
	ctx := context.Background()
	s := &Store{db: nil}
	_, err := s.GetEntityPercentages(ctx, nil, nil, nil, nil, 6)
	assert.Error(t, err)
}

func TestGetDeveloperPercentages(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)
	seedTestData(t, store)
	results, err := store.GetDeveloperPercentages(ctx, nil, nil, nil, []string{}, 12)
	require.NoError(t, err)
	assert.NotEmpty(t, results)
}

func TestGetEntityPercentages(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)
	seedTestData(t, store)
	results, err := store.GetEntityPercentages(ctx, nil, nil, nil, []string{}, 12)
	require.NoError(t, err)
	assert.NotEmpty(t, results)
}

func TestSearchDeveloperUsernames_NilDB(t *testing.T) {
	ctx := context.Background()
	s := &Store{db: nil}
	_, err := s.SearchDeveloperUsernames(ctx, "dev", nil, nil, 6, 10)
	assert.Error(t, err)
}

func TestSearchDeveloperUsernames_EmptyQuery(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)
	_, err := store.SearchDeveloperUsernames(ctx, "", nil, nil, 6, 10)
	assert.Error(t, err)
}

func TestSearchDeveloperUsernames_EmptyDB(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)
	results, err := store.SearchDeveloperUsernames(ctx, "dev", nil, nil, 6, 10)
	require.NoError(t, err)
	assert.Empty(t, results)
}

func TestSearchDeveloperUsernames_WithData(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)
	seedTestData(t, store)
	results, err := store.SearchDeveloperUsernames(ctx, "dev", nil, nil, 12, 10)
	require.NoError(t, err)
	assert.NotEmpty(t, results)
	for _, r := range results {
		assert.Contains(t, r, "dev")
	}
}
