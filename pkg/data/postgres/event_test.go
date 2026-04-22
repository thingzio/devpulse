package postgres

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/go-github/v83/github"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thingzio/devpulse/pkg/data"
)

func TestSplitBatch(t *testing.T) {
	tests := []struct {
		name     string
		total    int
		batchSz  int
		expected int
	}{
		{"exact", 100, 100, 1},
		{"remainder", 150, 100, 2},
		{"smaller", 50, 100, 1},
		{"zero", 0, 100, 0},
		{"large", 500, 100, 5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			events := make([]*data.Event, tt.total)
			batches := splitIntoBatches(events, tt.batchSz)
			assert.Equal(t, tt.expected, len(batches))
			total := 0
			for _, b := range batches {
				total += len(b)
			}
			assert.Equal(t, tt.total, total)
		})
	}
}

func TestSplitIntoBatchesContent(t *testing.T) {
	tests := []struct {
		name  string
		items []int
		size  int
		want  [][]int
	}{
		{"nil_input", nil, 5, nil},
		{"empty_input", []int{}, 5, nil},
		{"single_batch", []int{1, 2, 3}, 5, [][]int{{1, 2, 3}}},
		{"exact_batches", []int{1, 2, 3, 4}, 2, [][]int{{1, 2}, {3, 4}}},
		{"partial_last", []int{1, 2, 3}, 2, [][]int{{1, 2}, {3}}},
		{"single_element", []int{42}, 1, [][]int{{42}}},
		{"zero_size", []int{1, 2}, 0, nil},
		{"negative_size", []int{1, 2}, -1, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := splitIntoBatches(tt.items, tt.size)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestParseIssueNumberFromURL(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want int
	}{
		{name: "valid issues URL", url: "https://github.com/org/repo/issues/42", want: 42},
		{name: "with anchor fragment", url: "https://github.com/org/repo/issues/7#issuecomment-123", want: 7},
		{name: "no issues segment", url: "https://github.com/org/repo/pulls/5", want: 0},
		{name: "empty URL", url: "", want: 0},
		{name: "issues at end with no number", url: "https://github.com/org/repo/issues/", want: 0},
		{name: "non-numeric issue number", url: "https://github.com/org/repo/issues/abc", want: 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := parseIssueNumberFromURL(tc.url)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestParsePRNumberFromURL(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want int
	}{
		{name: "valid PR URL", url: "https://github.com/org/repo/pull/7", want: 7},
		{name: "empty URL", url: "", want: 0},
		{name: "non-numeric last segment", url: "https://github.com/org/repo/pull/abc", want: 0},
		{name: "trailing slash", url: "https://github.com/org/repo/pull/", want: 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := parsePRNumberFromURL(tc.url)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestTimestampToTime(t *testing.T) {
	t.Run("nil timestamp", func(t *testing.T) {
		assert.Nil(t, timestampToTime(nil))
	})

	t.Run("non-nil timestamp", func(t *testing.T) {
		now := time.Now().UTC()
		ts := &github.Timestamp{Time: now}
		got := timestampToTime(ts)
		require.NotNil(t, got)
		assert.Equal(t, now, *got)
	})
}

func TestTimestampStr(t *testing.T) {
	t.Run("nil timestamp", func(t *testing.T) {
		assert.Nil(t, timestampStr(nil))
	})

	t.Run("non-nil timestamp", func(t *testing.T) {
		fixed := time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC)
		ts := &github.Timestamp{Time: fixed}
		got := timestampStr(ts)
		require.NotNil(t, got)
		assert.Equal(t, "2024-06-15T12:00:00Z", *got)
	})
}

func TestIntPtr(t *testing.T) {
	t.Run("zero returns nil", func(t *testing.T) {
		assert.Nil(t, intPtr(0))
	})

	t.Run("non-zero returns pointer", func(t *testing.T) {
		p := intPtr(42)
		require.NotNil(t, p)
		assert.Equal(t, 42, *p)
	})
}

func TestGetStrPtr(t *testing.T) {
	t.Run("empty string returns nil", func(t *testing.T) {
		assert.Nil(t, getStrPtr(""))
	})

	t.Run("non-empty returns pointer", func(t *testing.T) {
		p := getStrPtr("hello")
		require.NotNil(t, p)
		assert.Equal(t, "hello", *p)
	})
}

func TestUnique(t *testing.T) {
	tests := []struct {
		name  string
		input []string
		want  []string
	}{
		{name: "nil input", input: nil, want: []string{}},
		{name: "no duplicates", input: []string{"a", "b", "c"}, want: []string{"a", "b", "c"}},
		{name: "with duplicates", input: []string{"a", "b", "a", "c"}, want: []string{"a", "b", "c"}},
		{name: "strips at-sign prefix", input: []string{"@user", "user"}, want: []string{"user"}},
		{name: "trims whitespace and deduplicates", input: []string{"  a  ", "a"}, want: []string{"a"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := unique(tc.input)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestIsEventBatchValidAge(t *testing.T) {
	minTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	e := &eventImporter{minEventTime: minTime}

	beforeMin := minTime.Add(-24 * time.Hour)
	afterMin := minTime.Add(24 * time.Hour)

	tests := []struct {
		name  string
		first *time.Time
		last  *time.Time
		want  bool
	}{
		{name: "nil first", first: nil, last: &afterMin, want: false},
		{name: "nil last", first: &afterMin, last: nil, want: false},
		{name: "both nil", first: nil, last: nil, want: false},
		{name: "both before min", first: &beforeMin, last: &beforeMin, want: false},
		{name: "first before, last after", first: &beforeMin, last: &afterMin, want: true},
		{name: "both after min", first: &afterMin, last: &afterMin, want: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := e.isEventBatchValidAge(tc.first, tc.last)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestFlushEmptyList(t *testing.T) {
	ctx := context.Background()

	// eventImporter with nil store -- flush must return before touching DB.
	imp := &eventImporter{
		list:   nil,
		users:  make(map[string]*github.User),
		state:  make(map[string]*data.State),
		counts: make(map[string]int),
	}

	// Empty list (nil): should return nil without DB access.
	require.NoError(t, imp.flush(ctx))
	assert.Equal(t, 0, imp.flushed)

	// Empty list (allocated but zero-length): same behavior.
	imp.list = make([]*data.Event, 0)
	require.NoError(t, imp.flush(ctx))
	assert.Equal(t, 0, imp.flushed)
}

func TestFlushSubBatching(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)

	origBatchSize := dbBatchSize
	dbBatchSize = 100
	t.Cleanup(func() { dbBatchSize = origBatchSize })

	const (
		numEvents = 300
		numUsers  = 50
		org       = "testorg"
		repo      = "testrepo"
	)

	users := make(map[string]*github.User, numUsers)
	for i := range numUsers {
		login := fmt.Sprintf("user-%03d", i)
		users[login] = &github.User{
			Login:   github.Ptr(login),
			Name:    github.Ptr("User " + login),
			HTMLURL: github.Ptr("https://github.com/" + login),
		}
	}

	events := make([]*data.Event, numEvents)
	baseTime := time.Now().UTC().Add(-time.Duration(numEvents) * time.Hour)
	for i := range numEvents {
		username := fmt.Sprintf("user-%03d", i%numUsers)
		evDate := baseTime.Add(time.Duration(i) * time.Hour)
		events[i] = &data.Event{
			Org:      org,
			Repo:     repo,
			Username: username,
			Type:     data.EventTypePR,
			Date:     evDate.Format("2006-01-02T15:04:05Z"),
			URL:      fmt.Sprintf("https://github.com/%s/%s/pull/%d", org, repo, i+1),
			Title:    fmt.Sprintf("PR #%d", i+1),
		}
	}

	since := baseTime.Add(-24 * time.Hour)
	state := map[string]*data.State{
		data.EventTypePR: {
			Since: since,
			Page:  1,
		},
	}

	imp := &eventImporter{
		store:  store,
		owner:  org,
		repo:   repo,
		list:   events,
		users:  users,
		state:  state,
		counts: make(map[string]int),
	}

	require.NoError(t, imp.flush(ctx))

	var eventCount int
	err := store.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM devpulse_event WHERE org = $1 AND repo = $2",
		org, repo).Scan(&eventCount)
	require.NoError(t, err)
	assert.Equal(t, numEvents, eventCount)

	var devCount int
	err = store.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM devpulse_developer WHERE username LIKE $1",
		"user-%").Scan(&devCount)
	require.NoError(t, err)
	assert.Equal(t, numUsers, devCount)

	var stateCount int
	err = store.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM devpulse_state WHERE org = $1 AND repo = $2",
		org, repo).Scan(&stateCount)
	require.NoError(t, err)
	assert.Equal(t, 1, stateCount, "expected one state row for event type pr")

	assert.Equal(t, numEvents, imp.flushed)
	assert.Empty(t, imp.list, "event list should be empty after flush")
}
