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

package server

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestFormatTimeSeriesLabelOrdering verifies multi-label series render in a
// stable, key-sorted order. Go map iteration is randomized, so without the
// sort the prefix could flip between "org repo" and "repo org" across calls.
func TestFormatTimeSeriesLabelOrdering(t *testing.T) {
	// org=acme, repo=widgets — keys sort as org < repo, so values render "acme widgets".
	raw := `{"timeSeries":[{"metric":{"labels":{"repo":"widgets","org":"acme"}},` +
		`"points":[{"interval":{"startTime":"2026-07-03T10:00:00Z"},"value":{"int64Value":"5"}}]}]}`

	// Deterministic across repeated calls despite randomized map iteration.
	first := formatTimeSeries(raw)
	for range 20 {
		assert.Equal(t, first, formatTimeSeries(raw), "output must be stable across calls")
	}
	assert.Contains(t, first, "acme widgets:", "labels must render in key-sorted order")
}

func TestFormatTimeSeries(t *testing.T) {
	tests := []struct {
		name     string
		raw      string
		contains []string
		equal    string
	}{
		{
			name:  "no series",
			raw:   `{"timeSeries":[]}`,
			equal: "  (no data)",
		},
		{
			name:  "invalid json",
			raw:   `{not json`,
			equal: "  (parse error)",
		},
		{
			name:     "double value",
			raw:      `{"timeSeries":[{"metric":{"labels":{}},"points":[{"interval":{"startTime":"2026-07-03T10:00:00Z"},"value":{"doubleValue":12.5}}]}]}`,
			contains: []string{"07-03T10:00=12.50"},
		},
		{
			name:     "int64 value parsed",
			raw:      `{"timeSeries":[{"metric":{"labels":{}},"points":[{"interval":{"startTime":"2026-07-03T10:00:00Z"},"value":{"int64Value":"42"}}]}]}`,
			contains: []string{"07-03T10:00=42.00"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatTimeSeries(tt.raw)
			if tt.equal != "" {
				assert.Equal(t, tt.equal, got)
			}
			for _, c := range tt.contains {
				assert.Contains(t, got, c)
			}
		})
	}
}

// TestFormatTimeSeriesPointLimit verifies the per-series cap of 8 points holds
// (renders at most 8 comma-separated values).
func TestFormatTimeSeriesPointLimit(t *testing.T) {
	pts := make([]string, 0, 20)
	for range 20 {
		pts = append(pts, `{"interval":{"startTime":"2026-07-03T10:00:00Z"},"value":{"int64Value":"1"}}`)
	}
	raw := `{"timeSeries":[{"metric":{"labels":{}},"points":[` + strings.Join(pts, ",") + `]}]}`
	got := formatTimeSeries(raw)
	assert.Equal(t, 8, strings.Count(got, "="), "should cap at 8 points per series")
}
