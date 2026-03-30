package postgres

import (
	"testing"
	"time"

	"github.com/google/go-github/v83/github"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
