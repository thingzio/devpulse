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
	"archive/zip"
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thingzio/devpulse/pkg/data"
	"github.com/thingzio/devpulse/pkg/plan"
	"github.com/thingzio/devpulse/pkg/tenant"
)

func TestCSVExportHandler_NoTenant(t *testing.T) {
	h := csvExportHandler(&mockStore{}, func(_ context.Context, _ string) ([]tenant.TenantRepo, error) {
		return nil, nil
	})

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/export/csv", nil)
	// No tenant in context.
	h.ServeHTTP(w, r)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Contains(t, w.Body.String(), "unauthorized")
}

func TestCSVExportHandler_FreePlanForbidden(t *testing.T) {
	h := csvExportHandler(&mockStore{}, func(_ context.Context, _ string) ([]tenant.TenantRepo, error) {
		return nil, nil
	})

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/export/csv", nil)
	r = withTenant(r, testTenant(plan.Free))
	h.ServeHTTP(w, r)

	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Contains(t, w.Body.String(), "CSV export is not available")
}

func TestCSVExportHandler_StarterPlanForbidden(t *testing.T) {
	h := csvExportHandler(&mockStore{}, func(_ context.Context, _ string) ([]tenant.TenantRepo, error) {
		return nil, nil
	})

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/export/csv", nil)
	r = withTenant(r, testTenant(plan.Starter))
	h.ServeHTTP(w, r)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestCSVExportHandler_ProPlanNoRepos(t *testing.T) {
	h := csvExportHandler(&mockStore{}, func(_ context.Context, _ string) ([]tenant.TenantRepo, error) {
		return nil, nil // no repos
	})

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/export/csv", nil)
	r = withTenant(r, testTenant(plan.Pro))
	h.ServeHTTP(w, r)

	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Contains(t, w.Body.String(), "no repos to export")
}

func TestCSVExportHandler_ProPlanInactiveReposOnly(t *testing.T) {
	h := csvExportHandler(&mockStore{}, func(_ context.Context, _ string) ([]tenant.TenantRepo, error) {
		return []tenant.TenantRepo{
			{Org: "o", Repo: "r", Active: false},
		}, nil
	})

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/export/csv", nil)
	r = withTenant(r, testTenant(plan.Pro))
	h.ServeHTTP(w, r)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestCSVExportHandler_ProPlanWithRepos(t *testing.T) {
	ms := &mockStore{
		getInsightsSummaryFn: func(_ context.Context, _, _, _ *string, _ int) (*data.InsightsSummary, error) {
			return &data.InsightsSummary{Events: 10, Contributors: 3, Repos: 1}, nil
		},
		getDeveloperPercentagesFn: func(_ context.Context, _, _, _ *string, _ []string, _ int) ([]*data.CountedItem, error) {
			return []*data.CountedItem{{Name: "dev1", Count: 50}}, nil
		},
	}

	listRepos := func(_ context.Context, _ string) ([]tenant.TenantRepo, error) {
		return []tenant.TenantRepo{
			{Org: "test-org", Repo: "test-repo", Active: true},
		}, nil
	}

	h := csvExportHandler(ms, listRepos)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/export/csv?d=90", nil)
	r = withTenant(r, testTenant(plan.Pro))
	h.ServeHTTP(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "application/zip", w.Header().Get("Content-Type"))
	assert.Contains(t, w.Header().Get("Content-Disposition"), "devpulse-test-org-test-repo-")
	assert.Contains(t, w.Header().Get("Content-Disposition"), ".zip")

	// Verify the zip is valid and contains expected files.
	zr, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
	require.NoError(t, err)

	fileNames := make([]string, 0, len(zr.File))
	for _, f := range zr.File {
		fileNames = append(fileNames, f.Name)
	}

	// summary.csv and developers.csv should be present (mock returns data).
	assert.True(t, containsSuffix(fileNames, "summary.csv"), "expected summary.csv in zip")
	assert.True(t, containsSuffix(fileNames, "developers.csv"), "expected developers.csv in zip")
}

func TestCSVExportHandler_EnterprisePlanMultiRepo(t *testing.T) {
	ms := &mockStore{
		getInsightsSummaryFn: func(_ context.Context, _, _, _ *string, _ int) (*data.InsightsSummary, error) {
			return &data.InsightsSummary{Events: 5}, nil
		},
	}

	listRepos := func(_ context.Context, _ string) ([]tenant.TenantRepo, error) {
		return []tenant.TenantRepo{
			{Org: "org1", Repo: "repo1", Active: true},
			{Org: "org2", Repo: "repo2", Active: true},
		}, nil
	}

	h := csvExportHandler(ms, listRepos)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/export/csv?d=90", nil)
	r = withTenant(r, testTenant(plan.Enterprise))
	h.ServeHTTP(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "application/zip", w.Header().Get("Content-Type"))
	// Multi-repo export uses generic filename.
	assert.Contains(t, w.Header().Get("Content-Disposition"), "devpulse-export-")

	zr, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
	require.NoError(t, err)
	// Should have entries for both repos.
	assert.GreaterOrEqual(t, len(zr.File), 2)
}

func TestCSVExportHandler_RepoFilter(t *testing.T) {
	ms := &mockStore{
		getInsightsSummaryFn: func(_ context.Context, _, _, _ *string, _ int) (*data.InsightsSummary, error) {
			return &data.InsightsSummary{Events: 1}, nil
		},
	}

	listRepos := func(_ context.Context, _ string) ([]tenant.TenantRepo, error) {
		return []tenant.TenantRepo{
			{Org: "org1", Repo: "repo1", Active: true},
			{Org: "org2", Repo: "repo2", Active: true},
		}, nil
	}

	h := csvExportHandler(ms, listRepos)

	// Request with specific repo filter.
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/export/csv?d=90&r=org1/repo1", nil)
	r = withTenant(r, testTenant(plan.Pro))
	h.ServeHTTP(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	// Single-repo filename.
	assert.Contains(t, w.Header().Get("Content-Disposition"), "devpulse-org1-repo1-")

	zr, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
	require.NoError(t, err)

	// All files should be prefixed with the org1-repo1 path.
	for _, f := range zr.File {
		assert.Contains(t, f.Name, "org1-repo1/")
	}
}

func TestCSVExportHandler_SummaryCSVContent(t *testing.T) {
	ms := &mockStore{
		getInsightsSummaryFn: func(_ context.Context, _, _, _ *string, _ int) (*data.InsightsSummary, error) {
			return &data.InsightsSummary{Events: 42, Contributors: 7, Repos: 3}, nil
		},
	}

	listRepos := func(_ context.Context, _ string) ([]tenant.TenantRepo, error) {
		return []tenant.TenantRepo{
			{Org: "o", Repo: "r", Active: true},
		}, nil
	}

	h := csvExportHandler(ms, listRepos)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/export/csv?d=90", nil)
	r = withTenant(r, testTenant(plan.Pro))
	h.ServeHTTP(w, r)

	require.Equal(t, http.StatusOK, w.Code)

	zr, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
	require.NoError(t, err)

	for _, f := range zr.File {
		if !hasSuffix(f.Name, "summary.csv") {
			continue
		}
		rc, err := f.Open()
		require.NoError(t, err)
		body, err := io.ReadAll(rc)
		rc.Close()
		require.NoError(t, err)
		csv := string(body)
		assert.Contains(t, csv, "total_events,42")
		assert.Contains(t, csv, "contributors,7")
		assert.Contains(t, csv, "repos,3")
		return
	}
	t.Fatal("summary.csv not found in zip")
}

// containsSuffix checks if any string in the slice ends with suffix.
func containsSuffix(ss []string, suffix string) bool {
	for _, s := range ss {
		if hasSuffix(s, suffix) {
			return true
		}
	}
	return false
}

func hasSuffix(s, suffix string) bool {
	return len(s) >= len(suffix) && s[len(s)-len(suffix):] == suffix
}
