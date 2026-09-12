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
	"github.com/thingzio/devpulse/pkg/middleware"
	"github.com/thingzio/devpulse/pkg/plan"
	"github.com/thingzio/devpulse/pkg/tenant"
)

// withTenant injects a tenant into the request context using the same key
// that middleware.RequireAuth uses, so TenantFromContext returns it.
func withTenant(r *http.Request, tn *tenant.Tenant) *http.Request {
	return r.WithContext(middleware.WithTenantContext(r.Context(), tn))
}

func testTenant(p string) *tenant.Tenant {
	return &tenant.Tenant{
		ID:       "t-test-001",
		GitHubID: 12345,
		Username: "testuser",
		Email:    "test@example.com",
		Plan:     p,
	}
}

// clearAPICache resets the package-level cache between tests.
func clearAPICache() {
	apiCache = &responseCache{}
}

func TestInsightsSummaryHandler(t *testing.T) {
	clearAPICache()

	called := 0
	ms := &mockStore{
		getInsightsSummaryFn: func(_ context.Context, _, _, _ *string, _ int) (*data.InsightsSummary, error) {
			called++
			return &data.InsightsSummary{Events: 42, Contributors: 5, Repos: 2}, nil
		},
	}

	h := insightsSummaryAPIHandler(ms)
	tn := testTenant(plan.Pro)

	// First call: cache miss.
	r := httptest.NewRequest(http.MethodGet, "/data/insights/summary?d=90&o=test-org&r=test-org/test-repo", nil)
	r = withTenant(r, tn)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))
	assert.Equal(t, 1, called)

	var body data.InsightsSummary
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, 42, body.Events)
	assert.Equal(t, 5, body.Contributors)

	// Second call: cache hit — store not called again.
	r2 := httptest.NewRequest(http.MethodGet, "/data/insights/summary?d=90&o=test-org&r=test-org/test-repo", nil)
	r2 = withTenant(r2, tn)
	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, r2)

	assert.Equal(t, http.StatusOK, w2.Code)
	assert.Equal(t, 1, called, "store should not be called on cache hit")
	assert.Equal(t, w.Body.String(), w2.Body.String())
}

func TestInsightsRepoMetricHistoryHandler(t *testing.T) {
	clearAPICache()

	ms := &mockStore{
		getRepoMetricHistoryFn: func(_ context.Context, _, _ *string, _ int) ([]*data.RepoMetricHistory, error) {
			return []*data.RepoMetricHistory{
				{Org: "test-org", Repo: "test-repo", Date: "2025-01-01", Stars: 100, Forks: 10},
			}, nil
		},
	}

	h := insightsRepoMetricHistoryAPIHandler(ms)

	r := httptest.NewRequest(http.MethodGet, "/data/insights/repo-metric-history?d=90&o=test-org&r=test-org/test-repo", nil)
	r = withTenant(r, testTenant(plan.Pro))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))

	var body []*data.RepoMetricHistory
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Len(t, body, 1)
	assert.Equal(t, 100, body[0].Stars)
}

func TestInsightWithEntityHandler_Parametric(t *testing.T) {
	tests := []struct {
		name    string
		factory func(data.Store) http.HandlerFunc
		result  any
	}{
		{
			name:    "insights summary",
			factory: insightsSummaryAPIHandler,
			result:  &data.InsightsSummary{Events: 10},
		},
		{
			name:    "daily activity",
			factory: insightsDailyActivityAPIHandler,
			result:  &data.DailyActivitySeries{},
		},
		{
			name:    "contributor retention",
			factory: insightsRetentionAPIHandler,
			result:  &data.RetentionSeries{},
		},
		{
			name:    "PR review ratio",
			factory: insightsPRRatioAPIHandler,
			result:  &data.PRReviewRatioSeries{},
		},
		{
			name:    "time to merge",
			factory: insightsTimeToMergeAPIHandler,
			result:  &data.VelocitySeries{},
		},
		{
			name:    "review latency",
			factory: insightsReviewLatencyAPIHandler,
			result:  &data.ReviewLatencySeries{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clearAPICache()

			// Each handler wraps insightWithEntityHandler which calls the
			// Store method through its fn closure. We create a handler that
			// returns tc.result directly by calling the factory with a mock.
			// The mock's default nil-returning stubs are fine because the
			// factory's closure captures the specific method.
			ms := &mockStore{}

			// Override the relevant function based on the factory.
			// For a parametric approach we use a generic insightWithEntityHandler
			// directly with a custom fn.
			h := insightWithEntityHandler(ms, tc.name, func(_ context.Context, _ data.Store, _, _, _ *string, _ int) (any, error) {
				return tc.result, nil
			})

			r := httptest.NewRequest(http.MethodGet, "/data/test?d=90", nil)
			r = withTenant(r, testTenant(plan.Starter))
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)

			assert.Equal(t, http.StatusOK, w.Code)
			assert.Equal(t, "application/json", w.Header().Get("Content-Type"))
			assert.Equal(t, browserCacheMaxAge, w.Header().Get(cacheControlHeaderKey))
			assert.NotEmpty(t, w.Body.Bytes())
		})
	}
}

func TestInsightHandler_Parametric(t *testing.T) {
	tests := []struct {
		name   string
		result any
	}{
		{"repo metric history", []*data.RepoMetricHistory{{Stars: 5}}},
		{"release downloads", &data.ReleaseDownloadsSeries{}},
		{"container activity", &data.ContainerActivitySeries{}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clearAPICache()

			h := insightHandler(nil, tc.name, func(_ context.Context, _ data.Store, _, _ *string, _ int) (any, error) {
				return tc.result, nil
			})

			r := httptest.NewRequest(http.MethodGet, "/data/test?d=90", nil)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)

			assert.Equal(t, http.StatusOK, w.Code)
			assert.Equal(t, "application/json", w.Header().Get("Content-Type"))
			assert.NotEmpty(t, w.Body.Bytes())
		})
	}
}

func TestInsightHandler_DaysCappedByPlan(t *testing.T) {
	clearAPICache()

	var capturedDays int
	h := insightWithEntityHandler(nil, "test", func(_ context.Context, _ data.Store, _, _, _ *string, days int) (any, error) {
		capturedDays = days
		return map[string]int{"ok": 1}, nil
	})

	// Free plan: MaxDataRangeMonths=3 => maxDays=90
	r := httptest.NewRequest(http.MethodGet, "/data/test?d=365", nil)
	r = withTenant(r, testTenant(plan.Free))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, 90, capturedDays, "days should be capped to free plan max (3mo=90d)")
}

func TestInsightHandler_StoreError(t *testing.T) {
	clearAPICache()

	h := insightWithEntityHandler(nil, "test-err", func(_ context.Context, _ data.Store, _, _, _ *string, _ int) (any, error) {
		return nil, assert.AnError
	})

	r := httptest.NewRequest(http.MethodGet, "/data/test?d=90", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Contains(t, w.Body.String(), "error querying test-err")
}

func TestInsightsGeneratedHandler_StripActionsForStarter(t *testing.T) {
	clearAPICache()

	ms := &mockStore{
		getRepoInsightsFn: func(_ context.Context, _, _ *string) ([]*data.RepoInsights, error) {
			return []*data.RepoInsights{
				{
					Org:  "o",
					Repo: "r",
					Insights: &data.GeneratedInsights{
						Observations: []data.InsightBullet{{Headline: "obs1"}},
						Actions:      []data.InsightBullet{{Headline: "act1"}},
					},
				},
			}, nil
		},
	}

	h := insightsGeneratedAPIHandler(ms)

	// Starter plan: AILevel=1 < 2, actions should be stripped.
	r := httptest.NewRequest(http.MethodGet, "/data/insights/generated?r=o/r", nil)
	r = withTenant(r, testTenant(plan.Starter))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	assert.Equal(t, http.StatusOK, w.Code)

	var body []*data.RepoInsights
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Len(t, body, 1)
	assert.Len(t, body[0].Insights.Observations, 1)
	assert.Nil(t, body[0].Insights.Actions, "actions should be stripped for starter plan")
}

func TestInsightsGeneratedHandler_KeepActionsForPro(t *testing.T) {
	clearAPICache()

	ms := &mockStore{
		getRepoInsightsFn: func(_ context.Context, _, _ *string) ([]*data.RepoInsights, error) {
			return []*data.RepoInsights{
				{
					Org:  "o",
					Repo: "r",
					Insights: &data.GeneratedInsights{
						Observations: []data.InsightBullet{{Headline: "obs1"}},
						Actions:      []data.InsightBullet{{Headline: "act1"}},
					},
				},
			}, nil
		},
	}

	h := insightsGeneratedAPIHandler(ms)

	// Pro plan: AILevel=2, actions should be kept.
	r := httptest.NewRequest(http.MethodGet, "/data/insights/generated?r=o/r", nil)
	r = withTenant(r, testTenant(plan.Pro))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	assert.Equal(t, http.StatusOK, w.Code)

	var body []*data.RepoInsights
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Len(t, body, 1)
	assert.Len(t, body[0].Insights.Actions, 1, "actions should be kept for pro plan")
}

// Verify middleware.TenantFromContext works with our withTenant helper.
func TestWithTenantHelper(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	tn := testTenant(plan.Free)
	r = withTenant(r, tn)

	got := middleware.TenantFromContext(r.Context())
	require.NotNil(t, got)
	assert.Equal(t, tn.ID, got.ID)
	assert.Equal(t, tn.Plan, got.Plan)
}
