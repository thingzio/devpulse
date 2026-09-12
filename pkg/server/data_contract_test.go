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
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thingzio/devpulse/pkg/data"
	"github.com/thingzio/devpulse/pkg/plan"
)

// TestDataAPIContracts is a contract test for the JSON shape returned by the
// public /data/insights/* endpoints. It guards against silent drift —
// renamed/removed JSON fields, accidental nil bodies, content-type changes —
// which would break the dashboard JS and the PDF report client even when
// every Go-level test still passes.
//
// For each endpoint:
//   - Stub the store with a populated response (mockStore default returns
//     yield empty but valid structures for most methods).
//   - Hit the handler and assert: 200 OK, application/json, valid JSON,
//     and a set of required top-level JSON keys.
//
// Required keys are deliberately a SUBSET of full struct fields — the test
// fails only on missing keys, never on additions, so adding new fields is
// safe.
func TestDataAPIContracts(t *testing.T) {
	clearAPICache()
	tn := testTenant(plan.Pro)

	ms := &mockStore{
		getInsightsSummaryFn: func(_ context.Context, _, _, _ *string, _ int) (*data.InsightsSummary, error) {
			return &data.InsightsSummary{BusFactor: 3, PonyFactor: 1, Events: 100, Contributors: 12, Repos: 1}, nil
		},
		getPortfolioSummaryFn: func(_ context.Context, _, _ *string, _ int) (*data.PortfolioSummary, error) {
			return &data.PortfolioSummary{TotalStars: 500, TotalForks: 100, TotalOpenIssues: 12, TotalContributors: 7}, nil
		},
		getRepoMetricHistoryFn: func(_ context.Context, _, _ *string, _ int) ([]*data.RepoMetricHistory, error) {
			return []*data.RepoMetricHistory{
				{Org: "o", Repo: "r", Date: "2026-04-01", Stars: 100, Forks: 10},
			}, nil
		},
		getEventTypeSeriesFn: func(_ context.Context, _, _, _ *string, _ int) (*data.EventTypeSeries, error) {
			return &data.EventTypeSeries{
				Dates:         []string{"2026-04"},
				PRs:           []int{5},
				Issues:        []int{3},
				PRReviews:     []int{7},
				IssueComments: []int{12},
				Forks:         []int{2},
				Total:         []int{29},
				Trend:         []float32{1.0},
			}, nil
		},
		getContributorCompositionFn: func(_ context.Context, _, _, _ *string, _ int) (*data.ContributorComposition, error) {
			return &data.ContributorComposition{Reviewers: 3, Authors: 5, Commenters: 7, Observers: 1}, nil
		},
	}

	cases := []struct {
		name         string
		path         string
		makeHandler  func() http.Handler
		requiredKeys []string
	}{
		{
			name:         "summary",
			path:         "/data/insights/summary?d=28&o=org&r=org/repo",
			makeHandler:  func() http.Handler { return insightsSummaryAPIHandler(ms) },
			requiredKeys: []string{"bus_factor", "pony_factor", "events", "contributors"},
		},
		{
			name:         "portfolio-summary",
			path:         "/data/insights/portfolio-summary?d=28",
			makeHandler:  func() http.Handler { return insightsPortfolioSummaryHandler(ms) },
			requiredKeys: []string{"total_stars", "total_forks", "total_contributors"},
		},
		{
			name:         "repo-metric-history",
			path:         "/data/insights/repo-metric-history?d=28&o=o&r=o/r",
			makeHandler:  func() http.Handler { return insightsRepoMetricHistoryAPIHandler(ms) },
			requiredKeys: nil, // returns array, not object
		},
		{
			name:         "type",
			path:         "/data/type?d=28&o=org&r=org/repo",
			makeHandler:  func() http.Handler { return eventDataAPIHandler(ms) },
			requiredKeys: []string{"dates", "pr", "issue", "pr_review", "issue_comment", "fork", "total"},
		},
		{
			name:         "contributor-composition",
			path:         "/data/insights/contributor-composition?d=28&o=org&r=org/repo",
			makeHandler:  func() http.Handler { return insightsContributorCompositionAPIHandler(ms) },
			requiredKeys: []string{"reviewers", "authors", "commenters", "observers"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearAPICache()

			r := httptest.NewRequest(http.MethodGet, tc.path, nil)
			r = withTenant(r, tn)
			w := httptest.NewRecorder()
			tc.makeHandler().ServeHTTP(w, r)

			require.Equal(t, http.StatusOK, w.Code, "endpoint must return 200, got body: %s", w.Body.String())
			assert.Equal(t, "application/json", w.Header().Get("Content-Type"), "must be JSON")
			assert.NotEqual(t, "null", w.Body.String(), "body must not be a bare null")

			// Validate JSON parses.
			var raw any
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &raw), "body must be valid JSON")

			// Validate required top-level keys (object responses only).
			if tc.requiredKeys != nil {
				obj, ok := raw.(map[string]any)
				require.True(t, ok, "expected JSON object, got %T", raw)
				for _, key := range tc.requiredKeys {
					_, present := obj[key]
					assert.True(t, present, "required key %q missing from %s response", key, tc.name)
				}
			}
		})
	}
}
