package server

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestComputeDelta_NilPrevious(t *testing.T) {
	current := platformStats{Tenants: 10, Repos: 5, Events: 100}
	got := computeDelta(current, nil)
	assert.Nil(t, got)
}

func TestComputeDelta_Normal(t *testing.T) {
	current := platformStats{
		Tenants:           12,
		TenantsFree:       8,
		TenantsStarter:    2,
		TenantsPro:        1,
		TenantsEnterprise: 1,
		Repos:             20,
		Events:            1500,
		Contributors:      50,
		Installations:     4,
		ReposWithErrors:   2,
	}
	prev := platformStats{
		Tenants:           10,
		TenantsFree:       7,
		TenantsStarter:    2,
		TenantsPro:        1,
		TenantsEnterprise: 0,
		Repos:             15,
		Events:            1000,
		Contributors:      40,
		Installations:     3,
		ReposWithErrors:   1,
	}

	got := computeDelta(current, &prev)
	require.NotNil(t, got)

	assert.Equal(t, 2, *got.Tenants)
	require.NotNil(t, got.TenantsPct)
	assert.InDelta(t, 20.0, *got.TenantsPct, 0.01)

	assert.Equal(t, 5, *got.Repos)
	require.NotNil(t, got.ReposPct)
	assert.InDelta(t, 33.33, *got.ReposPct, 0.01)

	assert.Equal(t, int64(500), *got.Events)
	require.NotNil(t, got.EventsPct)
	assert.InDelta(t, 50.0, *got.EventsPct, 0.01)

	assert.Equal(t, 10, *got.Contributors)
	require.NotNil(t, got.ContribPct)
	assert.InDelta(t, 25.0, *got.ContribPct, 0.01)

	assert.Equal(t, 1, *got.Installations)
	require.NotNil(t, got.InstallPct)
	assert.InDelta(t, 33.33, *got.InstallPct, 0.01)

	assert.Equal(t, 1, *got.ReposWithErrors)
	require.NotNil(t, got.ErrorsPct)
	assert.InDelta(t, 100.0, *got.ErrorsPct, 0.01)
}

func TestComputeDelta_ZeroPrevious(t *testing.T) {
	current := platformStats{
		Tenants:      5,
		Repos:        3,
		Events:       100,
		Contributors: 10,
	}
	prev := platformStats{} // all zeros

	got := computeDelta(current, &prev)
	require.NotNil(t, got)

	// Absolute deltas are set
	assert.Equal(t, 5, *got.Tenants)
	assert.Equal(t, 3, *got.Repos)
	assert.Equal(t, int64(100), *got.Events)
	assert.Equal(t, 10, *got.Contributors)

	// Percentages are nil (divide by zero guard)
	assert.Nil(t, got.TenantsPct)
	assert.Nil(t, got.ReposPct)
	assert.Nil(t, got.EventsPct)
	assert.Nil(t, got.ContribPct)
	assert.Nil(t, got.InstallPct)
	assert.Nil(t, got.ErrorsPct)
}

func TestComputeDelta_NoDifference(t *testing.T) {
	s := platformStats{
		Tenants:         10,
		Repos:           5,
		Events:          500,
		Contributors:    20,
		Installations:   3,
		ReposWithErrors: 1,
	}

	got := computeDelta(s, &s)
	require.NotNil(t, got)

	assert.Equal(t, 0, *got.Tenants)
	assert.Equal(t, 0, *got.Repos)
	assert.Equal(t, int64(0), *got.Events)
	assert.Equal(t, 0, *got.Contributors)
	assert.Equal(t, 0, *got.Installations)
	assert.Equal(t, 0, *got.ReposWithErrors)

	require.NotNil(t, got.TenantsPct)
	assert.Equal(t, 0.0, *got.TenantsPct)
	require.NotNil(t, got.ReposPct)
	assert.Equal(t, 0.0, *got.ReposPct)
	require.NotNil(t, got.EventsPct)
	assert.Equal(t, 0.0, *got.EventsPct)
}
