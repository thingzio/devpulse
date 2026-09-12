package postgres

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thingzio/devpulse/pkg/data"
)

func TestGetRepoMetricHistory_NilDB(t *testing.T) {
	ctx := context.Background()
	s := &Store{db: nil}
	_, err := s.GetRepoMetricHistory(ctx, nil, nil, 180)
	assert.Error(t, err)
}

func TestGetRepoMetricHistory_EmptyDB(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)
	list, err := store.GetRepoMetricHistory(ctx, nil, nil, 180)
	require.NoError(t, err)
	assert.Empty(t, list)
}

func TestGetRepoMetricHistory_WithData(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)

	d3, d2, d1 := daysAgo(3), daysAgo(2), daysAgo(1)
	_, err := store.db.ExecContext(ctx, fmt.Sprintf(`INSERT INTO devpulse_repo_metric_history(org, repo, date, stars, forks)
		VALUES
		('org1', 'repo1', '%s', 100, 50),
		('org1', 'repo1', '%s', 105, 52),
		('org1', 'repo1', '%s', 110, 55)`, d3, d2, d1))
	require.NoError(t, err)

	list, err := store.GetRepoMetricHistory(ctx, nil, nil, 180)
	require.NoError(t, err)
	require.Len(t, list, 3)
	assert.Equal(t, d3, list[0].Date)
	assert.Equal(t, 110, list[2].Stars)
	assert.Equal(t, 55, list[2].Forks)
}

func TestGetRepoMetricHistory_WithFilter(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)

	d1 := daysAgo(1)
	_, err := store.db.ExecContext(ctx, fmt.Sprintf(`INSERT INTO devpulse_repo_metric_history(org, repo, date, stars, forks)
		VALUES
		('org1', 'repo1', '%s', 100, 50),
		('org2', 'repo2', '%s', 200, 80)`, d1, d1))
	require.NoError(t, err)

	org := "org1"
	repo := "repo1"
	list, err := store.GetRepoMetricHistory(ctx, &org, &repo, 180)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, "org1", list[0].Org)
}

func TestGetRepoMetricHistory_AggregateByDate(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)

	d2, d1 := daysAgo(2), daysAgo(1)
	_, err := store.db.ExecContext(ctx, fmt.Sprintf(`INSERT INTO devpulse_repo_metric_history(org, repo, date, stars, forks)
		VALUES
		('org1', 'repo1', '%s', 100, 50),
		('org1', 'repo1', '%s', 105, 52),
		('org1', 'repo2', '%s', 200, 80),
		('org1', 'repo2', '%s', 210, 85)`, d2, d1, d2, d1))
	require.NoError(t, err)

	org := "org1"
	list, err := store.GetRepoMetricHistory(ctx, &org, nil, 180)
	require.NoError(t, err)
	require.Len(t, list, 2)
	assert.Equal(t, d2, list[0].Date)
	assert.Equal(t, 300, list[0].Stars)
	assert.Equal(t, 130, list[0].Forks)
	assert.Equal(t, d1, list[1].Date)
	assert.Equal(t, 315, list[1].Stars)
	assert.Equal(t, 137, list[1].Forks)
}

func TestBuildDailyTotals(t *testing.T) {
	starsByDay := map[string]int{
		time.Now().UTC().Format("2006-01-02"):                   5,
		time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02"): 3,
	}
	forksByDay := map[string]int{
		time.Now().UTC().Format("2006-01-02"): 2,
	}

	result := buildDailyTotals(100, 50, starsByDay, forksByDay, 3)

	require.Len(t, result, 4) // days+1
	assert.Equal(t, 100, result[3].Stars)
	assert.Equal(t, 50, result[3].Forks)
	assert.Equal(t, 95, result[2].Stars)
	assert.Equal(t, 48, result[2].Forks)
	assert.Equal(t, 92, result[1].Stars)
}

func TestUpsertMetricHistory(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)

	history := []*data.RepoMetricHistory{
		{Date: daysAgo(2), Stars: 100, Forks: 50},
		{Date: daysAgo(1), Stars: 105, Forks: 52},
	}

	err := store.upsertMetricHistory(ctx, "org1", "repo1", history)
	require.NoError(t, err)

	list, err := store.GetRepoMetricHistory(ctx, nil, nil, 180)
	require.NoError(t, err)
	require.Len(t, list, 2)
	assert.Equal(t, 100, list[0].Stars)

	// Upsert same dates with new values.
	history[0].Stars = 101
	err = store.upsertMetricHistory(ctx, "org1", "repo1", history)
	require.NoError(t, err)

	list, err = store.GetRepoMetricHistory(ctx, nil, nil, 180)
	require.NoError(t, err)
	require.Len(t, list, 2)
	assert.Equal(t, 101, list[0].Stars)
}

func TestBuildDailyTotals_FloorAtZero(t *testing.T) {
	starsByDay := map[string]int{
		time.Now().UTC().Format("2006-01-02"): 999,
	}
	result := buildDailyTotals(10, 5, starsByDay, nil, 1)
	require.Len(t, result, 2)
	assert.Equal(t, 10, result[1].Stars)
	assert.Equal(t, 0, result[0].Stars) // floored at zero
}
