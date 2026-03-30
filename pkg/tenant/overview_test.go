package tenant

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestStartOfWeekFrom(t *testing.T) {
	tests := []struct {
		name     string
		input    time.Time
		wantDay  time.Weekday
		wantHour int
	}{
		{
			name:    "Monday stays Monday",
			input:   time.Date(2024, 1, 8, 15, 30, 0, 0, time.UTC), // Mon
			wantDay: time.Monday,
		},
		{
			name:    "Wednesday goes back to Monday",
			input:   time.Date(2024, 1, 10, 9, 0, 0, 0, time.UTC), // Wed
			wantDay: time.Monday,
		},
		{
			name:    "Friday goes back to Monday",
			input:   time.Date(2024, 1, 12, 23, 59, 0, 0, time.UTC), // Fri
			wantDay: time.Monday,
		},
		{
			name:    "Saturday goes back to Monday",
			input:   time.Date(2024, 1, 13, 0, 0, 0, 0, time.UTC), // Sat
			wantDay: time.Monday,
		},
		{
			name:    "Sunday goes back to Monday (special case: weekday==0)",
			input:   time.Date(2024, 1, 14, 12, 0, 0, 0, time.UTC), // Sun
			wantDay: time.Monday,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := startOfWeekFrom(tc.input)
			assert.Equal(t, tc.wantDay, got.Weekday())
			// Should be truncated to midnight.
			assert.Equal(t, 0, got.Hour())
			assert.Equal(t, 0, got.Minute())
			assert.Equal(t, 0, got.Second())
		})
	}
}
