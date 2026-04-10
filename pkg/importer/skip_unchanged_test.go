package importer

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestShouldSkipUnchangedRepo(t *testing.T) {
	tests := []struct {
		name         string
		pushedAt     time.Time
		maxEventTime time.Time
		wantSkip     bool
	}{
		{
			name:         "no events yet, never skip",
			pushedAt:     time.Date(2025, 3, 20, 0, 0, 0, 0, time.UTC),
			maxEventTime: time.Time{},
			wantSkip:     false,
		},
		{
			name:         "no pushed_at, never skip",
			pushedAt:     time.Time{},
			maxEventTime: time.Date(2025, 3, 20, 0, 0, 0, 0, time.UTC),
			wantSkip:     false,
		},
		{
			name:         "pushed_at before max event, skip",
			pushedAt:     time.Date(2025, 3, 15, 0, 0, 0, 0, time.UTC),
			maxEventTime: time.Date(2025, 3, 20, 0, 0, 0, 0, time.UTC),
			wantSkip:     true,
		},
		{
			name:         "pushed_at equal to max event, skip",
			pushedAt:     time.Date(2025, 3, 20, 0, 0, 0, 0, time.UTC),
			maxEventTime: time.Date(2025, 3, 20, 0, 0, 0, 0, time.UTC),
			wantSkip:     true,
		},
		{
			name:         "pushed_at after max event, do not skip",
			pushedAt:     time.Date(2025, 3, 25, 0, 0, 0, 0, time.UTC),
			maxEventTime: time.Date(2025, 3, 20, 0, 0, 0, 0, time.UTC),
			wantSkip:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldSkipUnchangedRepo(tt.pushedAt, tt.maxEventTime)
			assert.Equal(t, tt.wantSkip, got)
		})
	}
}
