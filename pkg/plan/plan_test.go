package plan

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGet(t *testing.T) {
	tests := []struct {
		plan               string
		wantOK             bool
		wantMaxRepos       int
		wantMaxEvents      int
		wantDataRange      int
		wantAILevel        int
		wantDeepReputation bool
		wantPDFExport      bool
		wantCSVExport      bool
	}{
		{Free, true, 1, 500, 3, 0, false, false, false},
		{Starter, true, 5, 2500, 12, 1, false, true, false},
		{Pro, true, 25, 15000, 36, 2, true, true, true},
		{Enterprise, true, 0, 0, 0, 2, true, true, true},
		{"unknown", false, 0, 0, 0, 0, false, false, false},
		{"", false, 0, 0, 0, 0, false, false, false},
		{"FREE", false, 0, 0, 0, 0, false, false, false}, // case-sensitive
	}

	for _, tc := range tests {
		t.Run(tc.plan, func(t *testing.T) {
			limits, ok := Get(tc.plan)
			assert.Equal(t, tc.wantOK, ok)
			if tc.wantOK {
				require.True(t, ok)
				assert.Equal(t, tc.wantMaxRepos, limits.MaxRepos)
				assert.Equal(t, tc.wantMaxEvents, limits.MaxEventsPerWeek)
				assert.Equal(t, tc.wantDataRange, limits.MaxDataRangeMonths)
				assert.Equal(t, tc.wantAILevel, limits.AILevel)
				assert.Equal(t, tc.wantDeepReputation, limits.DeepReputation)
				assert.Equal(t, tc.wantPDFExport, limits.PDFExport)
				assert.Equal(t, tc.wantCSVExport, limits.CSVExport)
			}
		})
	}
}

func TestFreeLimits(t *testing.T) {
	l := FreeLimits()
	assert.Equal(t, 1, l.MaxRepos)
	assert.Equal(t, 500, l.MaxEventsPerWeek)
	assert.Equal(t, 3, l.MaxDataRangeMonths)
	assert.Equal(t, 0, l.AILevel)
	assert.False(t, l.DeepReputation)
	assert.False(t, l.PDFExport)
	assert.False(t, l.CSVExport)
}
