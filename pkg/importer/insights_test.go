package importer

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestCheckInsightStaleness(t *testing.T) {
	now := time.Now().UTC()

	tests := []struct {
		name         string
		generatedAt  string
		savedCount   int
		currentCount int
		want         bool
		wantReason   string
	}{
		{
			name:         "first generation (no prior insights)",
			generatedAt:  "",
			savedCount:   0,
			currentCount: 100,
			want:         true,
			wantReason:   "first generation",
		},
		{
			name:         "too recent (1 day old)",
			generatedAt:  now.Add(-24 * time.Hour).Format("2006-01-02T15:04:05Z"),
			savedCount:   1000,
			currentCount: 1200,
			want:         false,
			wantReason:   "days old",
		},
		{
			name:         "old enough but delta below threshold",
			generatedAt:  now.Add(-8 * 24 * time.Hour).Format("2006-01-02T15:04:05Z"),
			savedCount:   1000,
			currentCount: 1050,
			want:         false,
			wantReason:   "below threshold",
		},
		{
			name:         "old enough and delta above threshold",
			generatedAt:  now.Add(-8 * 24 * time.Hour).Format("2006-01-02T15:04:05Z"),
			savedCount:   1000,
			currentCount: 1150,
			want:         true,
			wantReason:   "exceeds threshold",
		},
		{
			name:         "old enough with zero saved count",
			generatedAt:  now.Add(-8 * 24 * time.Hour).Format("2006-01-02T15:04:05Z"),
			savedCount:   0,
			currentCount: 50,
			want:         true,
			wantReason:   "no prior event count",
		},
		{
			name:         "old enough with events decreasing beyond threshold",
			generatedAt:  now.Add(-8 * 24 * time.Hour).Format("2006-01-02T15:04:05Z"),
			savedCount:   1000,
			currentCount: 850,
			want:         true,
			wantReason:   "exceeds threshold",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, reason := checkInsightStaleness(tc.generatedAt, tc.savedCount, tc.currentCount)
			assert.Equal(t, tc.want, got)
			assert.Contains(t, reason, tc.wantReason)
		})
	}
}
