// Copyright 2026 Thingz LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
// SPDX-License-Identifier: Apache-2.0

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
		wantDataRange int
		wantAILevel   int
		wantPDFExport bool
		wantCSVExport bool
	}{
		{Free, true, 1, 500, 3, 0, false, false},
		{Starter, true, 5, 2500, 6, 1, true, false},
		{Pro, true, 25, 15000, 12, 2, true, true},
		{Enterprise, true, 0, 0, 0, 2, true, true},
		{"unknown", false, 0, 0, 0, 0, false, false},
		{"", false, 0, 0, 0, 0, false, false},
		{"FREE", false, 0, 0, 0, 0, false, false}, // case-sensitive
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
	assert.False(t, l.PDFExport)
	assert.False(t, l.CSVExport)
}

func TestDisplayPlans(t *testing.T) {
	dp := DisplayPlans()
	require.Len(t, dp, 4)
	assert.Equal(t, "Free", dp[0].DisplayName)
	assert.Equal(t, "Starter", dp[1].DisplayName)
	assert.Equal(t, "Pro", dp[2].DisplayName)
	assert.Equal(t, "Enterprise", dp[3].DisplayName)
}

func TestDisplayFeatures(t *testing.T) {
	features := DisplayFeatures()
	require.Len(t, features, 11)

	// Spot-check feature IDs and labels.
	assert.Equal(t, "feature-repos", features[0].ID)
	assert.Equal(t, "Repos", features[0].Label)
	require.Len(t, features[0].Values, 4)
	assert.Equal(t, "1", features[0].Values[0])
	assert.Equal(t, "Unlimited", features[0].Values[3])

	// AI row.
	ai := features[6]
	assert.Equal(t, "feature-ai", ai.ID)
	assert.Equal(t, "AI", ai.Label)
	assert.Equal(t, "\u2014", ai.Values[0])
	assert.Equal(t, "Insights", ai.Values[1])
	assert.Equal(t, "Insights + Actions", ai.Values[2])

	// Every feature row has 4 values (one per plan).
	for _, f := range features {
		assert.Len(t, f.Values, 4, "feature %s should have 4 values", f.ID)
		assert.False(t, f.Span, "feature %s should not span", f.ID)
	}
}

func TestGetName(t *testing.T) {
	p, ok := Get(Pro)
	require.True(t, ok)
	assert.Equal(t, "pro", p.Name)
	assert.Equal(t, "Pro", p.DisplayName)
}

func TestFormatMaxRepos(t *testing.T) {
	free := FreeLimits()
	assert.Equal(t, "1", free.FormatMaxRepos())

	ent, _ := Get(Enterprise)
	assert.Equal(t, "unlimited", ent.FormatMaxRepos())
}

func TestFormatMaxEvents(t *testing.T) {
	free := FreeLimits()
	assert.Equal(t, "500", free.FormatMaxEvents())

	ent, _ := Get(Enterprise)
	assert.Equal(t, "unlimited", ent.FormatMaxEvents())
}
