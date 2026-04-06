package postgres

import (
	"slices"
	"strings"
	"testing"

	"github.com/google/go-github/v83/github"
	"github.com/stretchr/testify/assert"

	"github.com/thingzio/devpulse/pkg/data"
)

func TestDeveloperSortByUsername(t *testing.T) {
	devs := []*data.Developer{
		{Username: "charlie"},
		{Username: "alice"},
		{Username: "bob"},
		{Username: "alice"}, // duplicate
	}

	slices.SortFunc(devs, func(a, b *data.Developer) int {
		return strings.Compare(a.Username, b.Username)
	})

	want := []string{"alice", "alice", "bob", "charlie"}
	got := make([]string, len(devs))
	for i, d := range devs {
		got[i] = d.Username
	}
	assert.Equal(t, want, got)
}

func TestEventSortByPK(t *testing.T) {
	events := []*data.Event{
		{Org: "org1", Repo: "repoB", Username: "alice", Type: "pr", Date: "2025-01-02"},
		{Org: "org1", Repo: "repoA", Username: "bob", Type: "issue", Date: "2025-01-01"},
		{Org: "org1", Repo: "repoA", Username: "alice", Type: "pr", Date: "2025-01-01"},
		{Org: "org1", Repo: "repoA", Username: "alice", Type: "issue", Date: "2025-01-02"},
		{Org: "org1", Repo: "repoA", Username: "alice", Type: "issue", Date: "2025-01-01"},
	}

	slices.SortFunc(events, compareEventsByPK)

	type key struct{ org, repo, user, typ, date string }
	want := []key{
		{"org1", "repoA", "alice", "issue", "2025-01-01"},
		{"org1", "repoA", "alice", "issue", "2025-01-02"},
		{"org1", "repoA", "alice", "pr", "2025-01-01"},
		{"org1", "repoA", "bob", "issue", "2025-01-01"},
		{"org1", "repoB", "alice", "pr", "2025-01-02"},
	}
	got := make([]key, len(events))
	for i, e := range events {
		got[i] = key{e.Org, e.Repo, e.Username, e.Type, e.Date}
	}
	assert.Equal(t, want, got)
}

func TestReleaseSortByTag(t *testing.T) {
	tag := func(s string) *github.RepositoryRelease {
		return &github.RepositoryRelease{TagName: github.Ptr(s)}
	}

	releases := []*github.RepositoryRelease{
		tag("v2.0.0"),
		tag("v1.0.0"),
		tag("v1.1.0"),
	}

	slices.SortFunc(releases, func(a, b *github.RepositoryRelease) int {
		return strings.Compare(a.GetTagName(), b.GetTagName())
	})

	want := []string{"v1.0.0", "v1.1.0", "v2.0.0"}
	got := make([]string, len(releases))
	for i, r := range releases {
		got[i] = r.GetTagName()
	}
	assert.Equal(t, want, got)
}

func TestReleaseAssetSortByName(t *testing.T) {
	asset := func(s string) *github.ReleaseAsset {
		return &github.ReleaseAsset{Name: github.Ptr(s)}
	}

	assets := []*github.ReleaseAsset{
		asset("cli-linux-arm64.tar.gz"),
		asset("cli-darwin-amd64.tar.gz"),
		asset("cli-linux-amd64.tar.gz"),
	}

	slices.SortFunc(assets, func(a, b *github.ReleaseAsset) int {
		return strings.Compare(a.GetName(), b.GetName())
	})

	want := []string{"cli-darwin-amd64.tar.gz", "cli-linux-amd64.tar.gz", "cli-linux-arm64.tar.gz"}
	got := make([]string, len(assets))
	for i, a := range assets {
		got[i] = a.GetName()
	}
	assert.Equal(t, want, got)
}

func TestMetricHistorySortByDate(t *testing.T) {
	history := []*data.RepoMetricHistory{
		{Date: "2025-01-15"},
		{Date: "2025-01-01"},
		{Date: "2025-01-10"},
	}

	slices.SortFunc(history, func(a, b *data.RepoMetricHistory) int {
		return strings.Compare(a.Date, b.Date)
	})

	want := []string{"2025-01-01", "2025-01-10", "2025-01-15"}
	got := make([]string, len(history))
	for i, h := range history {
		got[i] = h.Date
	}
	assert.Equal(t, want, got)
}

func TestSortStability_AlreadySorted(t *testing.T) {
	devs := []*data.Developer{
		{Username: "alice"},
		{Username: "bob"},
		{Username: "charlie"},
	}

	slices.SortFunc(devs, func(a, b *data.Developer) int {
		return strings.Compare(a.Username, b.Username)
	})

	want := []string{"alice", "bob", "charlie"}
	got := make([]string, len(devs))
	for i, d := range devs {
		got[i] = d.Username
	}
	assert.Equal(t, want, got)
}

func TestSortStability_SingleElement(t *testing.T) {
	devs := []*data.Developer{{Username: "solo"}}

	slices.SortFunc(devs, func(a, b *data.Developer) int {
		return strings.Compare(a.Username, b.Username)
	})

	assert.Equal(t, "solo", devs[0].Username)
}

func TestSortStability_Empty(t *testing.T) {
	var devs []*data.Developer

	slices.SortFunc(devs, func(a, b *data.Developer) int {
		return strings.Compare(a.Username, b.Username)
	})

	assert.Empty(t, devs)
}
