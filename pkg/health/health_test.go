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

package health

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGrade(t *testing.T) {
	tests := []struct {
		name  string
		score float64
		want  string
	}{
		{"perfect", 100, "A"},
		{"high A", 95, "A"},
		{"low A", 90, "A"},
		{"high B", 89.9, "B"},
		{"mid B", 80, "B"},
		{"low B", 70, "B"},
		{"high C", 69.9, "C"},
		{"mid C", 60, "C"},
		{"low C", 50, "C"},
		{"high D", 49.9, "D"},
		{"mid D", 40, "D"},
		{"low D", 30, "D"},
		{"high F", 29.9, "F"},
		{"zero", 0, "F"},
		{"negative clamped", -5, "F"},
		{"over 100", 110, "A"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, Grade(tc.score))
		})
	}
}

func TestDemandScore(t *testing.T) {
	t.Run("all zeros", func(t *testing.T) {
		score := DemandScore(DemandInput{})
		assert.InDelta(t, 50, score, 1.0)
	})

	t.Run("strong positive growth", func(t *testing.T) {
		score := DemandScore(DemandInput{
			StarGrowthPct:            20,
			ExternalContributorDelta: 10,
			NewPRDelta:               15,
			NewIssueDelta:            10,
		})
		assert.Greater(t, score, 80.0)
		assert.LessOrEqual(t, score, 100.0)
	})

	t.Run("negative signals", func(t *testing.T) {
		score := DemandScore(DemandInput{
			StarGrowthPct:            -5,
			ExternalContributorDelta: -3,
			NewPRDelta:               -10,
			NewIssueDelta:            -5,
		})
		assert.Less(t, score, 50.0)
		assert.GreaterOrEqual(t, score, 0.0)
	})
}

func TestThroughputScore(t *testing.T) {
	t.Run("excellent throughput", func(t *testing.T) {
		score := ThroughputScore(ThroughputInput{
			MedianMergeHours: 2,
			PRBacklogDelta:   -5,
			AgingPRsPct:      0,
		})
		assert.Greater(t, score, 80.0)
		assert.LessOrEqual(t, score, 100.0)
	})

	t.Run("poor throughput", func(t *testing.T) {
		score := ThroughputScore(ThroughputInput{
			MedianMergeHours: 200,
			PRBacklogDelta:   20,
			AgingPRsPct:      80,
		})
		assert.Less(t, score, 30.0)
		assert.GreaterOrEqual(t, score, 0.0)
	})

	t.Run("zero values", func(t *testing.T) {
		score := ThroughputScore(ThroughputInput{})
		assert.Greater(t, score, 50.0)
	})
}

func TestResponsivenessScore(t *testing.T) {
	t.Run("highly responsive", func(t *testing.T) {
		score := ResponsivenessScore(ResponsivenessInput{
			FirstResponsePRHours:    1,
			FirstResponseIssueHours: 2,
			RespondedWithin48hPct:   95,
			UnansweredPct:           2,
		})
		assert.Greater(t, score, 80.0)
		assert.LessOrEqual(t, score, 100.0)
	})

	t.Run("unresponsive", func(t *testing.T) {
		score := ResponsivenessScore(ResponsivenessInput{
			FirstResponsePRHours:    200,
			FirstResponseIssueHours: 300,
			RespondedWithin48hPct:   10,
			UnansweredPct:           80,
		})
		assert.Less(t, score, 30.0)
		assert.GreaterOrEqual(t, score, 0.0)
	})
}

func TestOverall(t *testing.T) {
	t.Run("equal scores", func(t *testing.T) {
		assert.InDelta(t, 75.0, Overall(75, 75, 75), 0.01)
	})

	t.Run("mixed scores", func(t *testing.T) {
		assert.InDelta(t, 60.0, Overall(90, 50, 40), 0.01)
	})

	t.Run("all zero", func(t *testing.T) {
		assert.InDelta(t, 0.0, Overall(0, 0, 0), 0.01)
	})

	t.Run("all perfect", func(t *testing.T) {
		assert.InDelta(t, 100.0, Overall(100, 100, 100), 0.01)
	})
}

func TestClamp(t *testing.T) {
	assert.Equal(t, 0.0, clamp(-10))
	assert.Equal(t, 100.0, clamp(150))
	assert.Equal(t, 50.0, clamp(50))
}

func TestSubScore(t *testing.T) {
	assert.InDelta(t, 12.5, subScore(0, 10), 0.01)
	assert.InDelta(t, 25.0, subScore(10, 10), 0.01)
	assert.InDelta(t, 0.0, subScore(-100, 10), 0.5)
	assert.Greater(t, subScore(5, 10), 12.5)
	assert.Less(t, subScore(5, 10), 25.0)
}

func TestInverseScore(t *testing.T) {
	assert.InDelta(t, 100.0, inverseScore(0, 168), 0.01)
	assert.InDelta(t, 0.0, inverseScore(168, 168), 0.01)
	assert.InDelta(t, 0.0, inverseScore(200, 168), 0.01)
	assert.InDelta(t, 50.0, inverseScore(84, 168), 0.5)
}
