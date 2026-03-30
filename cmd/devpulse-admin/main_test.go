package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolvePlanLimits(t *testing.T) {
	tests := []struct {
		plan          string
		wantOK        bool
		wantMaxRepos  int
		wantMaxEvents int
	}{
		{"free", true, 5, 2000},
		{"pro", true, 25, 20000},
		{"enterprise", true, 100, 100000},
		{"unknown", false, 0, 0},
		{"", false, 0, 0},
		{"FREE", false, 0, 0}, // case-sensitive
	}

	for _, tc := range tests {
		t.Run(tc.plan, func(t *testing.T) {
			limits, ok := resolvePlanLimits(tc.plan)
			assert.Equal(t, tc.wantOK, ok)
			if tc.wantOK {
				require.True(t, ok)
				assert.Equal(t, tc.wantMaxRepos, limits[0])
				assert.Equal(t, tc.wantMaxEvents, limits[1])
			}
		})
	}
}
