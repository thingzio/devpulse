package postgres

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetRepoLike(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)
	seedTestData(t, store)
	items, err := store.GetRepoLike(ctx, "test", 10)
	require.NoError(t, err)
	assert.NotEmpty(t, items)
	assert.Contains(t, items[0].Value, "testorg/testrepo")
}

func TestGetRepoLike_EmptyQuery(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)
	_, err := store.GetRepoLike(ctx, "", 10)
	assert.Error(t, err)
}

func TestGetRepoLike_NilDB(t *testing.T) {
	ctx := context.Background()
	s := &Store{db: nil}
	_, err := s.GetRepoLike(ctx, "test", 10)
	assert.Error(t, err)
}

func TestGetRepoLike_NoResults(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)
	seedTestData(t, store)
	items, err := store.GetRepoLike(ctx, "nonexistent", 10)
	require.NoError(t, err)
	assert.Empty(t, items)
}
