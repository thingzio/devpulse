package postgres

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thingzio/devpulse/pkg/data"
)

func TestSaveAndGetDeveloper(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)
	devs := []*data.Developer{
		{Username: "testuser", FullName: "Test User", Email: "test@example.com", Entity: "TESTCORP"},
	}
	require.NoError(t, store.SaveDevelopers(ctx, devs))

	got, err := store.GetDeveloper(ctx, "testuser")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "testuser", got.Username)
	assert.Equal(t, "TESTCORP", got.Entity)
}

func TestGetDeveloper_NotFound(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)
	got, err := store.GetDeveloper(ctx, "nonexistent")
	assert.NoError(t, err)
	assert.Nil(t, got)
}

func TestSearchDevelopers(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)
	devs := []*data.Developer{
		{Username: "alice", FullName: "Alice Smith", Email: "alice@corp.com", Entity: "CORP"},
		{Username: "bob", FullName: "Bob Jones", Email: "bob@other.com", Entity: "OTHER"},
	}
	require.NoError(t, store.SaveDevelopers(ctx, devs))

	results, err := store.SearchDevelopers(ctx, "alice", 10)
	require.NoError(t, err)
	assert.Len(t, results, 1)
	assert.Equal(t, "alice", results[0].Username)
}

func TestSaveDevelopers_Upsert(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)
	devs := []*data.Developer{{Username: "user1", FullName: "Original", Entity: "CORP"}}
	require.NoError(t, store.SaveDevelopers(ctx, devs))

	devs[0].FullName = "Updated"
	require.NoError(t, store.SaveDevelopers(ctx, devs))

	got, err := store.GetDeveloper(ctx, "user1")
	require.NoError(t, err)
	assert.Equal(t, "Updated", got.FullName)
}

func TestSaveDevelopers_EmptySlice(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)
	assert.NoError(t, store.SaveDevelopers(ctx, []*data.Developer{}))
}

func TestSaveDevelopers_NilDB(t *testing.T) {
	ctx := context.Background()
	s := &Store{db: nil}
	err := s.SaveDevelopers(ctx, []*data.Developer{{Username: "test"}})
	assert.Error(t, err)
}

func TestGetDeveloper_NilDB(t *testing.T) {
	ctx := context.Background()
	s := &Store{db: nil}
	_, err := s.GetDeveloper(ctx, "test")
	assert.Error(t, err)
}

func TestSearchDevelopers_NilDB(t *testing.T) {
	ctx := context.Background()
	s := &Store{db: nil}
	_, err := s.SearchDevelopers(ctx, "test", 10)
	assert.Error(t, err)
}

func TestGetDeveloperUsernames(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)
	devs := []*data.Developer{
		{Username: "user1", FullName: "User One"},
		{Username: "user2", FullName: "User Two"},
	}
	require.NoError(t, store.SaveDevelopers(ctx, devs))

	usernames, err := store.GetDeveloperUsernames(ctx)
	require.NoError(t, err)
	assert.Len(t, usernames, 2)
}

func TestUpdateDeveloperNames(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)
	devs := []*data.Developer{
		{Username: "user1", FullName: ""},
	}
	require.NoError(t, store.SaveDevelopers(ctx, devs))

	names := map[string]string{"user1": "Updated Name"}
	require.NoError(t, store.UpdateDeveloperNames(ctx, names))

	got, err := store.GetDeveloper(ctx, "user1")
	require.NoError(t, err)
	assert.Equal(t, "Updated Name", got.FullName)
}

func TestGetNoFullnameDeveloperUsernames(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)
	devs := []*data.Developer{
		{Username: "withname", FullName: "Has Name"},
		{Username: "noname", FullName: ""},
	}
	require.NoError(t, store.SaveDevelopers(ctx, devs))

	usernames, err := store.GetNoFullnameDeveloperUsernames(ctx)
	require.NoError(t, err)
	assert.Len(t, usernames, 1)
	assert.Equal(t, "noname", usernames[0])
}

func TestGetUnenrichedDeveloperUsernames(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)
	devs := []*data.Developer{
		{Username: "with_entity", FullName: "Has Entity", Entity: "CORP"},
		{Username: "no_entity", FullName: "No Entity"},
	}
	require.NoError(t, store.SaveDevelopers(ctx, devs))

	// with_entity has entity='CORP' → stored as 'CORP'
	// no_entity has entity='' → NULLIF → stored as NULL → unenriched
	usernames, err := store.GetUnenrichedDeveloperUsernames(ctx)
	require.NoError(t, err)
	assert.Len(t, usernames, 1)
	assert.Equal(t, "no_entity", usernames[0])
}

func TestGetDeveloperUsernames_NilDB(t *testing.T) {
	ctx := context.Background()
	s := &Store{db: nil}
	_, err := s.GetDeveloperUsernames(ctx)
	assert.Error(t, err)
}

func TestUpdateDeveloperNames_NilDB(t *testing.T) {
	ctx := context.Background()
	s := &Store{db: nil}
	err := s.UpdateDeveloperNames(ctx, map[string]string{"u": "n"})
	assert.Error(t, err)
}

func TestSaveDevelopers_Concurrent(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)

	// Two batches with overlapping developers — must not deadlock.
	batch1 := []*data.Developer{
		{Username: "alice", FullName: "Alice A", Email: "a@x.com"},
		{Username: "bob", FullName: "Bob B", Email: "b@x.com"},
		{Username: "shared", FullName: "Shared V1", Email: "s@x.com"},
	}
	batch2 := []*data.Developer{
		{Username: "shared", FullName: "Shared V2", Email: "s@y.com"},
		{Username: "carol", FullName: "Carol C", Email: "c@x.com"},
	}

	var wg sync.WaitGroup
	errs := make([]error, 2)
	wg.Add(2)
	go func() { defer wg.Done(); errs[0] = store.SaveDevelopers(ctx, batch1) }()
	go func() { defer wg.Done(); errs[1] = store.SaveDevelopers(ctx, batch2) }()
	wg.Wait()

	require.NoError(t, errs[0], "batch1")
	require.NoError(t, errs[1], "batch2")

	// All 4 unique developers exist.
	for _, name := range []string{"alice", "bob", "carol", "shared"} {
		dev, err := store.GetDeveloper(ctx, name)
		require.NoError(t, err)
		require.NotNil(t, dev, "developer %s should exist", name)
	}
}

func TestSearchDevelopers_MultipleMatches(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)
	devs := []*data.Developer{
		{Username: "alice1", FullName: "Alice A", Entity: "CORP"},
		{Username: "alice2", FullName: "Alice B", Entity: "CORP"},
		{Username: "bob", FullName: "Bob", Entity: "CORP"},
	}
	require.NoError(t, store.SaveDevelopers(ctx, devs))

	results, err := store.SearchDevelopers(ctx, "alice", 10)
	require.NoError(t, err)
	assert.Len(t, results, 2)
}
