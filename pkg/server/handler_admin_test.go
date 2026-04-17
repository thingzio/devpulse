package server

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thingzio/devpulse/pkg/plan"
)

func TestResolveGitHubUserID_InvalidUser(t *testing.T) {
	ctx := context.Background()
	_, err := resolveGitHubUserID(ctx, "this-user-definitely-does-not-exist-xyz-999")
	require.Error(t, err)
}

func TestResolveGitHubUserID_EmptyUsername(t *testing.T) {
	ctx := context.Background()
	// Empty username results in a 404 from GitHub API.
	_, err := resolveGitHubUserID(ctx, "")
	require.Error(t, err)
}

func TestAdminDashboardData(t *testing.T) {
	data := adminDashboardData{
		Title: "Admin",
		Summary: summaryResponse{
			Date: "2026-04-17",
			Current: platformStats{
				Tenants: 10,
				Repos:   25,
				Events:  50000,
			},
		},
		CSRFToken: "test-token",
	}

	assert.Equal(t, "Admin", data.Title)
	assert.Equal(t, "2026-04-17", data.Summary.Date)
	assert.Equal(t, 10, data.Summary.Current.Tenants)
	assert.Equal(t, "test-token", data.CSRFToken)
}

func TestAdminTenantsData(t *testing.T) {
	data := adminTenantsData{
		Title: "Tenants",
		Tenants: []tenantSummary{
			{Username: "alice", Plan: "pro", MaxRepos: 25},
			{Username: "bob", Plan: "free", MaxRepos: 1},
		},
	}

	assert.Equal(t, "Tenants", data.Title)
	assert.Len(t, data.Tenants, 2)
	assert.Equal(t, "alice", data.Tenants[0].Username)
	assert.Equal(t, "free", data.Tenants[1].Plan)
}

func TestAdminTenantDetailData(t *testing.T) {
	data := adminTenantDetailData{
		Title: "Tenant: alice",
		Detail: tenantDetail{
			Username: "alice",
			Plan:     "pro",
			Repos: []repoDetail{
				{Name: "org/repo", Events: 1000, WeeklyEvents: 50},
			},
		},
		Plans:     plan.All,
		CSRFToken: "csrf-abc",
	}

	assert.Equal(t, "Tenant: alice", data.Title)
	assert.Equal(t, "alice", data.Detail.Username)
	assert.Len(t, data.Detail.Repos, 1)
	assert.Equal(t, "org/repo", data.Detail.Repos[0].Name)
	assert.NotEmpty(t, data.Plans)
	_, ok := data.Plans["pro"]
	assert.True(t, ok)
}

func TestAdminTokensData(t *testing.T) {
	data := adminTokensData{
		Title: "Token Status",
		Tokens: []tokenStatus{
			{Login: "nvidia", InstallationID: 123, Limit: 5000, Remaining: 4500},
		},
	}

	assert.Len(t, data.Tokens, 1)
	assert.Equal(t, "nvidia", data.Tokens[0].Login)
	assert.Equal(t, 4500, data.Tokens[0].Remaining)
}

func TestAdminMetricsData(t *testing.T) {
	data := adminMetricsData{
		Title:    "Metrics Review",
		Days:     2,
		Metrics:  "some raw metrics",
		Analysis: "everything looks healthy",
	}

	assert.Equal(t, 2, data.Days)
	assert.Equal(t, "some raw metrics", data.Metrics)
	assert.Equal(t, "everything looks healthy", data.Analysis)
}
