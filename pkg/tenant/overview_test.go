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

package tenant

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStartOfWeekFrom(t *testing.T) {
	tests := []struct {
		name     string
		input    time.Time
		wantDay  time.Weekday
		wantHour int
	}{
		{
			name:    "Monday stays Monday",
			input:   time.Date(2024, 1, 8, 15, 30, 0, 0, time.UTC), // Mon
			wantDay: time.Monday,
		},
		{
			name:    "Wednesday goes back to Monday",
			input:   time.Date(2024, 1, 10, 9, 0, 0, 0, time.UTC), // Wed
			wantDay: time.Monday,
		},
		{
			name:    "Friday goes back to Monday",
			input:   time.Date(2024, 1, 12, 23, 59, 0, 0, time.UTC), // Fri
			wantDay: time.Monday,
		},
		{
			name:    "Saturday goes back to Monday",
			input:   time.Date(2024, 1, 13, 0, 0, 0, 0, time.UTC), // Sat
			wantDay: time.Monday,
		},
		{
			name:    "Sunday goes back to Monday (special case: weekday==0)",
			input:   time.Date(2024, 1, 14, 12, 0, 0, 0, time.UTC), // Sun
			wantDay: time.Monday,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := startOfWeekFrom(tc.input)
			assert.Equal(t, tc.wantDay, got.Weekday())
			// Should be truncated to midnight.
			assert.Equal(t, 0, got.Hour())
			assert.Equal(t, 0, got.Minute())
			assert.Equal(t, 0, got.Second())
		})
	}
}

func TestGetOverview_SampleReposExcludedFromUsage(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	tn, err := UpsertTenant(ctx, db, 80001, "overviewuser", "", "", "", "", "", "")
	require.NoError(t, err)

	// Add one sample repo and one regular repo
	require.NoError(t, AddSampleRepos(ctx, db, tn.ID, []OrgRepo{{Org: "sorg", Repo: "srepo"}}))
	require.NoError(t, AddTenantRepos(ctx, db, tn.ID, []OrgRepo{{Org: "rorg", Repo: "rrepo"}}))

	// Insert a developer (FK requirement for events)
	_, err = db.ExecContext(ctx, `INSERT INTO devpulse_developer (username, full_name) VALUES ('dev1', 'Dev One')`)
	require.NoError(t, err)

	// Insert events for both repos — dates within this week and within 30 days
	today := time.Now().UTC().Format("2006-01-02")
	for _, r := range []struct{ org, repo string }{{"sorg", "srepo"}, {"rorg", "rrepo"}} {
		for i := range 3 {
			_, err = db.ExecContext(ctx,
				`INSERT INTO devpulse_event (org, repo, username, type, date, url, mentions, labels, title)
				 VALUES ($1, $2, 'dev1', $3, $4, '', '', '', '')`,
				r.org, r.repo, fmt.Sprintf("pr_%d", i), today)
			require.NoError(t, err)
		}
	}

	resp, err := GetOverview(ctx, db, tn.ID, 30)
	require.NoError(t, err)

	// Should have 2 repos
	require.Len(t, resp.Repos, 2)

	var sampleRepo, regularRepo *RepoOverview
	for i := range resp.Repos {
		if resp.Repos[i].Sample {
			sampleRepo = &resp.Repos[i]
		} else {
			regularRepo = &resp.Repos[i]
		}
	}
	require.NotNil(t, sampleRepo, "expected a sample repo in overview")
	require.NotNil(t, regularRepo, "expected a regular repo in overview")

	// Sample repo should have Sample=true and events counted
	assert.True(t, sampleRepo.Sample)
	assert.Equal(t, 3, sampleRepo.Events)

	// Regular repo should have Sample=false
	assert.False(t, regularRepo.Sample)
	assert.Equal(t, 3, regularRepo.Events)

	// Usage summary: ActiveRepos should exclude sample repos
	assert.Equal(t, 1, resp.Usage.ActiveRepos)

	// Usage summary: WeeklyEvents should only count regular repo events
	assert.Equal(t, 3, resp.Usage.WeeklyEvents)
}

func TestGetOverview_NoRepos(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	tn, err := UpsertTenant(ctx, db, 80002, "emptyoverview", "", "", "", "", "", "")
	require.NoError(t, err)

	resp, err := GetOverview(ctx, db, tn.ID, 30)
	require.NoError(t, err)
	assert.Empty(t, resp.Repos)
	assert.Equal(t, 0, resp.Usage.ActiveRepos)
	assert.Equal(t, 0, resp.Usage.WeeklyEvents)
}

func TestGetOverview_OnlySampleRepos(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	tn, err := UpsertTenant(ctx, db, 80003, "onlysamples", "", "", "", "", "", "")
	require.NoError(t, err)

	require.NoError(t, AddSampleRepos(ctx, db, tn.ID, []OrgRepo{
		{Org: "s1", Repo: "r1"},
		{Org: "s2", Repo: "r2"},
	}))

	resp, err := GetOverview(ctx, db, tn.ID, 30)
	require.NoError(t, err)
	assert.Len(t, resp.Repos, 2)
	assert.Equal(t, 0, resp.Usage.ActiveRepos)
	assert.Equal(t, 0, resp.Usage.WeeklyEvents)
	assert.False(t, resp.Usage.LimitReached)
}

func TestGetWeeklyEventCount_ExcludesSampleRepos(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	tn, err := UpsertTenant(ctx, db, 80004, "weeklyuser", "", "", "", "", "", "")
	require.NoError(t, err)

	// Add one sample repo and one regular repo
	require.NoError(t, AddSampleRepos(ctx, db, tn.ID, []OrgRepo{{Org: "sorg", Repo: "sw"}}))
	require.NoError(t, AddTenantRepos(ctx, db, tn.ID, []OrgRepo{{Org: "rorg", Repo: "rw"}}))

	// Insert developer
	_, err = db.ExecContext(ctx, `INSERT INTO devpulse_developer (username, full_name) VALUES ('dev2', 'Dev Two')`)
	require.NoError(t, err)

	// Insert events this week for both repos
	today := time.Now().UTC().Format("2006-01-02")
	for _, r := range []struct{ org, repo string }{{"sorg", "sw"}, {"rorg", "rw"}} {
		for i := range 5 {
			_, err = db.ExecContext(ctx,
				`INSERT INTO devpulse_event (org, repo, username, type, date, url, mentions, labels, title)
				 VALUES ($1, $2, 'dev2', $3, $4, '', '', '', '')`,
				r.org, r.repo, fmt.Sprintf("issue_%d", i), today)
			require.NoError(t, err)
		}
	}

	count, err := GetWeeklyEventCount(ctx, db, tn.ID)
	require.NoError(t, err)
	// Only regular repo events should be counted
	assert.Equal(t, 5, count)
}

// The contributor counts in tenantRepoOverviewSQL are computed by a grouped
// join rather than a per-repo LATERAL with a correlated EXISTS. These are the
// semantics that rewrite has to preserve: fork events and bot accounts are
// excluded from both counts, and "scored" additionally requires the developer
// to carry a reputation.
func TestGetOverview_ContributorAndScoredCounts(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	tn, err := UpsertTenant(ctx, db, 80007, "countsuser", "", "", "", "", "", "")
	require.NoError(t, err)
	require.NoError(t, AddTenantRepos(ctx, db, tn.ID, []OrgRepo{{Org: "corg", Repo: "crepo"}}))

	// scoreddev has a reputation, plaindev does not, dependabot[bot] is a bot.
	for _, d := range []struct {
		name string
		rep  any
	}{
		{"scoreddev", 4.2},
		{"plaindev", nil},
		{"dependabot[bot]", 3.0},
		{"copilot", 3.0},
	} {
		_, err = db.ExecContext(ctx,
			`INSERT INTO devpulse_developer (username, full_name, reputation) VALUES ($1, $1, $2)`,
			d.name, d.rep)
		require.NoError(t, err)
	}

	today := time.Now().UTC().Format("2006-01-02")
	// (username, type) pairs — type varies to satisfy the event primary key.
	events := []struct{ user, typ string }{
		{"scoreddev", "pr"},       // counts toward contribs and scored
		{"plaindev", "pr"},        // contribs only: no reputation
		{"scoreddev", "fork"},     // excluded: fork
		{"dependabot[bot]", "pr"}, // excluded: bot suffix
		{"copilot", "pr"},         // excluded: known bot name
	}
	for _, e := range events {
		_, err = db.ExecContext(ctx,
			`INSERT INTO devpulse_event (org, repo, username, type, date, url, mentions, labels, title)
			 VALUES ('corg', 'crepo', $1, $2, $3, '', '', '', '')`,
			e.user, e.typ, today)
		require.NoError(t, err)
	}

	resp, err := GetOverview(ctx, db, tn.ID, 30)
	require.NoError(t, err)
	require.Len(t, resp.Repos, 1)
	r := resp.Repos[0]

	assert.Equal(t, 5, r.Events, "Events counts every row, including forks and bots")
	assert.Equal(t, 5, r.WeeklyEvents, "all events are dated today")
	assert.Equal(t, 2, r.Contributors, "scoreddev + plaindev; forks and bots excluded")
	assert.Equal(t, 1, r.Scored, "only scoreddev has a reputation")
}

// A repo with no events at all must still appear, with zeroed counts -- the
// grouped subquery produces no row for it, so the LEFT JOIN + COALESCE has to
// carry it.
func TestGetOverview_RepoWithNoEvents(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	tn, err := UpsertTenant(ctx, db, 80008, "noeventsuser", "", "", "", "", "", "")
	require.NoError(t, err)
	require.NoError(t, AddTenantRepos(ctx, db, tn.ID, []OrgRepo{{Org: "eorg", Repo: "erepo"}}))

	resp, err := GetOverview(ctx, db, tn.ID, 30)
	require.NoError(t, err)
	require.Len(t, resp.Repos, 1)

	r := resp.Repos[0]
	assert.Equal(t, "eorg", r.Org)
	assert.Zero(t, r.Events)
	assert.Zero(t, r.WeeklyEvents)
	assert.Zero(t, r.Contributors)
	assert.Zero(t, r.Scored)
}
