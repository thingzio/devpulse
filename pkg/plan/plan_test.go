package plan

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGet(t *testing.T) {
	tests := []struct {
		plan          string
		wantOK        bool
		wantMaxRepos  int
		wantMaxEvents int
	}{
		{Free, true, 3, 1000},
		{Pro, true, 15, 15000},
		{Enterprise, true, 0, 0}, // 0 = unlimited
		{"unknown", false, 0, 0},
		{"", false, 0, 0},
		{"FREE", false, 0, 0}, // case-sensitive
	}

	for _, tc := range tests {
		t.Run(tc.plan, func(t *testing.T) {
			limits, ok := Get(tc.plan)
			assert.Equal(t, tc.wantOK, ok)
			if tc.wantOK {
				require.True(t, ok)
				assert.Equal(t, tc.wantMaxRepos, limits.MaxRepos)
				assert.Equal(t, tc.wantMaxEvents, limits.MaxEventsPerWeek)
			}
		})
	}
}

func TestFreeLimits(t *testing.T) {
	l := FreeLimits()
	assert.Equal(t, 3, l.MaxRepos)
	assert.Equal(t, 1000, l.MaxEventsPerWeek)
}
