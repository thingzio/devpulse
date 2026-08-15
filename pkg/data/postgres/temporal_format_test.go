package postgres

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thingzio/devpulse/pkg/data"
)

// Temporal columns are stored as native DATE/TIMESTAMPTZ but exposed by the
// JSON/YAML/CSV APIs as strings. These tests pin the exact wire format of
// every such field so a change to the storage type, the driver's time.Time
// rendering, or the session TimeZone fails loudly here rather than silently
// altering what clients receive.
//
// dateLayout   "2006-01-02"
// tsLayoutGo   "2006-01-02T15:04:05Z"
const (
	goldenDate = "2026-03-01"
	goldenTS   = "2026-03-01T14:22:11Z"
	goldenTS2  = "2026-03-05T09:03:00Z"
)

func TestTemporalWireFormat_Event(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()

	_, err := store.db.ExecContext(ctx,
		`INSERT INTO devpulse_developer (username, full_name, email, avatar, url, entity)
		 VALUES ('gopher', 'Go Pher', 'g@example.com', 'a', 'u', 'ent')`)
	require.NoError(t, err)

	_, err = store.db.ExecContext(ctx,
		`INSERT INTO devpulse_event
		   (org, repo, username, type, date, url, mentions, labels, number, created_at, closed_at, merged_at)
		 VALUES ('o', 'r', 'gopher', 'pr', $1, 'u', '', '', 1, $2, $3, $3)`,
		goldenDate, goldenTS, goldenTS2)
	require.NoError(t, err)

	got, err := store.SearchEvents(ctx, &data.EventSearchCriteria{Page: 1, PageSize: 10})
	require.NoError(t, err)
	require.Len(t, got, 1)
	e := got[0].Event

	assert.Equal(t, goldenDate, e.Date, "Event.Date must stay YYYY-MM-DD")
	require.NotNil(t, e.CreatedAt)
	assert.Equal(t, goldenTS, *e.CreatedAt, "Event.CreatedAt must stay RFC3339 seconds")
	require.NotNil(t, e.ClosedAt)
	assert.Equal(t, goldenTS2, *e.ClosedAt)
	require.NotNil(t, e.MergedAt)
	assert.Equal(t, goldenTS2, *e.MergedAt)
}

// A sub-second value must still render without fractional digits: production
// data has none today, but nothing in the schema prevents one appearing.
func TestTemporalWireFormat_TruncatesSubSecond(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()

	_, err := store.db.ExecContext(ctx,
		`INSERT INTO devpulse_developer (username, full_name, email, avatar, url, entity)
		 VALUES ('gopher', 'Go Pher', 'g@example.com', 'a', 'u', 'ent')`)
	require.NoError(t, err)

	_, err = store.db.ExecContext(ctx,
		`INSERT INTO devpulse_event
		   (org, repo, username, type, date, url, mentions, labels, number, created_at)
		 VALUES ('o', 'r', 'gopher', 'pr', $1, 'u', '', '', 1, '2026-03-01T14:22:11.123456Z')`,
		goldenDate)
	require.NoError(t, err)

	got, err := store.SearchEvents(ctx, &data.EventSearchCriteria{Page: 1, PageSize: 10})
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.NotNil(t, got[0].Event.CreatedAt)
	assert.Equal(t, goldenTS, *got[0].Event.CreatedAt,
		"sub-second precision must not leak into the wire format")
}

func TestTemporalWireFormat_RepoMetaAndOverview(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()

	_, err := store.db.ExecContext(ctx,
		`INSERT INTO devpulse_repo_meta (org, repo, language, license, updated_at, last_import_at, pushed_at)
		 VALUES ('o', 'r', 'Go', 'MIT', $1, $1, NULL)`, goldenTS)
	require.NoError(t, err)

	metas, err := store.GetRepoMetas(ctx, nil, nil)
	require.NoError(t, err)
	require.Len(t, metas, 1)
	assert.Equal(t, goldenTS, metas[0].UpdatedAt, "RepoMeta.UpdatedAt must stay RFC3339 seconds")

	overview, err := store.GetRepoOverview(ctx, nil, 3650)
	require.NoError(t, err)
	require.Len(t, overview, 1)
	assert.Equal(t, goldenTS, overview[0].LastImport, "RepoOverview.LastImport must stay RFC3339 seconds")
}

// A NULL timestamp must surface as "" — the sentinel the API and templates
// already treat as "never imported".
func TestTemporalWireFormat_NullRendersEmpty(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()

	_, err := store.db.ExecContext(ctx,
		`INSERT INTO devpulse_repo_meta (org, repo, language, license, updated_at, last_import_at)
		 VALUES ('o', 'r', 'Go', 'MIT', NULL, NULL)`)
	require.NoError(t, err)

	metas, err := store.GetRepoMetas(ctx, nil, nil)
	require.NoError(t, err)
	require.Len(t, metas, 1)
	assert.Empty(t, metas[0].UpdatedAt, "NULL updated_at must render as empty string")

	overview, err := store.GetRepoOverview(ctx, nil, 3650)
	require.NoError(t, err)
	require.Len(t, overview, 1)
	assert.Empty(t, overview[0].LastImport, "NULL last_import_at must render as empty string")
}

func TestTemporalWireFormat_RepoInsights(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()

	_, err := store.db.ExecContext(ctx,
		`INSERT INTO devpulse_repo_insights (org, repo, insights_json, model, generated_at, event_count)
		 VALUES ('o', 'r', '{}', 'm', $1, 5)`, goldenTS)
	require.NoError(t, err)

	list, err := store.GetRepoInsights(ctx, nil, nil)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, goldenTS, list[0].GeneratedAt, "RepoInsights.GeneratedAt must stay RFC3339 seconds")

	at, err := store.GetRepoInsightsGeneratedAt(ctx, "o", "r")
	require.NoError(t, err)
	assert.Equal(t, goldenTS, at)
}

func TestTemporalWireFormat_MetricHistory(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()

	org, repo := "o", "r"
	_, err := store.db.ExecContext(ctx,
		`INSERT INTO devpulse_repo_metric_history (org, repo, date, stars, forks)
		 VALUES ($1, $2, $3, 1, 2)`, org, repo, goldenDate)
	require.NoError(t, err)

	hist, err := store.GetRepoMetricHistory(ctx, &org, &repo, 3650)
	require.NoError(t, err)
	require.NotEmpty(t, hist)

	var found bool
	for _, h := range hist {
		if h.Date == goldenDate {
			found = true
		}
	}
	assert.True(t, found, "RepoMetricHistory.Date must stay YYYY-MM-DD, got %+v", hist[0])
}

func TestTemporalWireFormat_MinEventDate(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()

	_, err := store.db.ExecContext(ctx,
		`INSERT INTO devpulse_developer (username, full_name, email, avatar, url, entity)
		 VALUES ('gopher', 'Go Pher', 'g@example.com', 'a', 'u', 'ent')`)
	require.NoError(t, err)
	_, err = store.db.ExecContext(ctx,
		`INSERT INTO devpulse_event (org, repo, username, type, date, url, mentions, labels)
		 VALUES ('o', 'r', 'gopher', 'pr', $1, 'u', '', '')`, goldenDate)
	require.NoError(t, err)

	min, err := store.GetMinEventDate(ctx, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, goldenDate, min, "GetMinEventDate must stay YYYY-MM-DD")
}

func TestTemporalWireFormat_MinEventDateEmptyDB(t *testing.T) {
	store := setupTestDB(t)

	min, err := store.GetMinEventDate(context.Background(), nil, nil)
	require.NoError(t, err)
	assert.Empty(t, min, "no rows must yield the empty-string sentinel, not an error")
}
