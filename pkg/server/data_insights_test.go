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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thingzio/devpulse/pkg/data"
)

func TestMonthOverMonthDelta(t *testing.T) {
	tests := []struct {
		name  string
		valid bool
		prev  int
		curr  int
		want  float64
	}{
		{name: "valid false returns 0", valid: false, prev: 10, curr: 15, want: 0},
		{name: "prev zero returns 0", valid: true, prev: 0, curr: 15, want: 0},
		{name: "normal positive delta", valid: true, prev: 10, curr: 15, want: 50.0},
		{name: "negative delta", valid: true, prev: 20, curr: 10, want: -50.0},
		{name: "no change", valid: true, prev: 10, curr: 10, want: 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := monthOverMonthDelta(tc.valid, func() (int, int) {
				return tc.prev, tc.curr
			})
			assert.InDelta(t, tc.want, got, 0.001)
		})
	}
}

func TestBuildDemandInput_AllNil(t *testing.T) {
	di := buildDemandInput(nil, nil, nil, nil)
	assert.Equal(t, 0.0, di.StarGrowthPct)
	assert.Equal(t, 0.0, di.ExternalContributorDelta)
	assert.Equal(t, 0.0, di.NewPRDelta)
	assert.Equal(t, 0.0, di.NewIssueDelta)
}

func TestBuildDemandInput_MetricHistory(t *testing.T) {
	history := []*data.RepoMetricHistory{
		{Stars: 100, Forks: 10},
		{Stars: 150, Forks: 15},
	}
	di := buildDemandInput(history, nil, nil, nil)
	// (150-100)/100 * 100 = 50%
	assert.InDelta(t, 50.0, di.StarGrowthPct, 0.001)
}

func TestBuildDemandInput_MetricHistorySmallStars(t *testing.T) {
	history := []*data.RepoMetricHistory{
		{Stars: 5, Forks: 0},
		{Stars: 8, Forks: 0},
	}
	di := buildDemandInput(history, nil, nil, nil)
	// ref.Stars < 10, gain = 8-5 = 3
	assert.InDelta(t, 3.0, di.StarGrowthPct, 0.001)
}

func TestBuildDemandInput_Momentum(t *testing.T) {
	momentum := &data.MomentumSeries{
		Labels: []string{"w1", "w2", "w3"},
		Active: []int{10, 20, 30},
		Delta:  []int{0, 10, 10},
	}
	di := buildDemandInput(nil, momentum, nil, nil)
	// (30-20)/20 * 100 = 50%
	assert.InDelta(t, 50.0, di.ExternalContributorDelta, 0.001)
}

func TestBuildDemandInput_PRAndIssueRatio(t *testing.T) {
	pr := &data.PRReviewRatioSeries{
		Labels: []string{"w1", "w2"},
		PRs:    []int{10, 15},
	}
	issue := &data.IssueRatioSeries{
		Labels: []string{"w1", "w2"},
		Opened: []int{20, 10},
	}
	di := buildDemandInput(nil, nil, pr, issue)
	assert.InDelta(t, 50.0, di.NewPRDelta, 0.001)
	assert.InDelta(t, -50.0, di.NewIssueDelta, 0.001)
}

func TestBuildThroughputInput_Nil(t *testing.T) {
	ti := buildThroughputInput(nil, nil)
	assert.Equal(t, 0.0, ti.MedianMergeHours)
	assert.Equal(t, 0.0, ti.AgingPRsPct)
	assert.Equal(t, 0.0, ti.PRBacklogDelta)
}

func TestBuildThroughputInput_Populated(t *testing.T) {
	ttm := &data.VelocitySeries{
		Labels:  []string{"w1", "w2"},
		AvgDays: []float64{1.5, 2.0},
		Count:   []int{10, 15},
	}
	aging := &data.AgingPRsSeries{
		TotalOpen:  20,
		Over30Days: 5,
		AgingPct:   25.0,
	}
	ti := buildThroughputInput(ttm, aging)
	// MedianMergeHours = last AvgDays * 24 = 2.0 * 24 = 48
	assert.InDelta(t, 48.0, ti.MedianMergeHours, 0.001)
	assert.InDelta(t, 25.0, ti.AgingPRsPct, 0.001)
	// PRBacklogDelta = (15-10)/10 * 100 = 50
	assert.InDelta(t, 50.0, ti.PRBacklogDelta, 0.001)
}

func TestBuildResponsivenessInput_AllNil(t *testing.T) {
	ri := buildResponsivenessInput(nil, nil, nil)
	assert.Equal(t, 0.0, ri.FirstResponsePRHours)
	assert.Equal(t, 0.0, ri.FirstResponseIssueHours)
	assert.Equal(t, 0.0, ri.RespondedWithin48hPct)
	assert.Equal(t, 0.0, ri.UnansweredPct)
}

func TestBuildResponsivenessInput_Populated(t *testing.T) {
	ttfr := &data.FirstResponseSeries{
		Labels:   []string{"w1", "w2"},
		PRAvg:    []float64{4.5, 6.0},
		IssueAvg: []float64{8.0, 12.0},
	}
	slo := &data.ResponseSLOSeries{
		TotalItems:   100,
		WithinSLO:    85,
		WithinSLOPct: 85.0,
	}
	unanswered := &data.UnansweredSeries{
		TotalItems:    50,
		Unanswered:    10,
		UnansweredPct: 20.0,
	}
	ri := buildResponsivenessInput(ttfr, slo, unanswered)
	assert.InDelta(t, 6.0, ri.FirstResponsePRHours, 0.001)
	assert.InDelta(t, 12.0, ri.FirstResponseIssueHours, 0.001)
	assert.InDelta(t, 85.0, ri.RespondedWithin48hPct, 0.001)
	assert.InDelta(t, 20.0, ri.UnansweredPct, 0.001)
}

func TestBuildScorecard_AllNil(t *testing.T) {
	sc := buildScorecard(nil, nil, nil, nil, nil, nil, nil, nil, nil)
	require.NotNil(t, sc)
	assert.NotEmpty(t, sc.Overall, "grade should be set even with zero inputs")
	assert.Equal(t, "Demand", sc.Demand.Name)
	assert.Equal(t, "Throughput", sc.Throughput.Name)
	assert.Equal(t, "Responsiveness", sc.Responsiveness.Name)
	assert.NotNil(t, sc.Demand.Metrics)
	assert.NotNil(t, sc.Throughput.Metrics)
	assert.NotNil(t, sc.Responsiveness.Metrics)
}

func TestBuildScorecard_Populated(t *testing.T) {
	momentum := &data.MomentumSeries{
		Labels: []string{"w1", "w2"},
		Active: []int{10, 15},
		Delta:  []int{0, 5},
	}
	ttm := &data.VelocitySeries{
		Labels:  []string{"w1", "w2"},
		AvgDays: []float64{1.0, 0.5},
		Count:   []int{20, 18},
	}
	ttfr := &data.FirstResponseSeries{
		Labels:   []string{"w1"},
		PRAvg:    []float64{3.0},
		IssueAvg: []float64{5.0},
	}
	history := []*data.RepoMetricHistory{
		{Stars: 100, Forks: 10},
		{Stars: 120, Forks: 12},
	}
	aging := &data.AgingPRsSeries{AgingPct: 10.0}
	unanswered := &data.UnansweredSeries{UnansweredPct: 5.0}
	slo := &data.ResponseSLOSeries{WithinSLOPct: 90.0}
	prRatio := &data.PRReviewRatioSeries{
		Labels: []string{"w1", "w2"},
		PRs:    []int{10, 12},
	}
	issueRatio := &data.IssueRatioSeries{
		Labels: []string{"w1", "w2"},
		Opened: []int{5, 8},
	}

	sc := buildScorecard(momentum, ttm, ttfr, history, aging, unanswered, slo, prRatio, issueRatio)
	require.NotNil(t, sc)
	assert.NotEmpty(t, sc.Overall)
	assert.Greater(t, sc.OverallScore, 0.0)
	assert.Greater(t, sc.Demand.Score, 0.0)
	assert.Greater(t, sc.Throughput.Score, 0.0)
	assert.Greater(t, sc.Responsiveness.Score, 0.0)
}
