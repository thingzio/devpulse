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
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seedTenantRepo inserts a tenant + tenant_repo with the given active flag.
func seedTenantRepo(t *testing.T, db *sql.DB, ctx context.Context, ghID int64, username, org, repo string, active bool) {
	t.Helper()

	var tenantID string
	err := db.QueryRowContext(ctx, `
		INSERT INTO devpulse_tenant (github_id, username, plan)
		VALUES ($1, $2, 'pro')
		ON CONFLICT (github_id) DO UPDATE SET username = EXCLUDED.username
		RETURNING id`, ghID, username).Scan(&tenantID)
	require.NoError(t, err)

	_, err = db.ExecContext(ctx, `
		INSERT INTO devpulse_tenant_repo (tenant_id, org, repo, active)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (tenant_id, org, repo) DO UPDATE SET active = EXCLUDED.active`,
		tenantID, org, repo, active)
	require.NoError(t, err)
}

// seedDeveloper inserts a developer row. Idempotent across calls with the
// same username.
func seedDeveloper(t *testing.T, db *sql.DB, ctx context.Context, username string) {
	t.Helper()
	_, err := db.ExecContext(ctx, `
		INSERT INTO devpulse_developer (username, full_name)
		VALUES ($1, $1)
		ON CONFLICT (username) DO NOTHING`, username)
	require.NoError(t, err)
}

// seedEvents inserts exactly `count` events of the given type for (org, repo),
// each from a unique synthetic user (so the (org,repo,username,type,date) PK
// never collides). All events are dated today so they fall inside the
// 63-day staleness window. usernamePrefix=="bot" produces bot-style names
// like "bot_001[bot]" to exercise the bot-exclude filter.
func seedEvents(t *testing.T, db *sql.DB, ctx context.Context, org, repo, usernamePrefix, evType string, count int) {
	t.Helper()

	today := time.Now().UTC().Format("2006-01-02")
	for i := 0; i < count; i++ {
		username := fmt.Sprintf("%s_%03d", usernamePrefix, i)
		if strings.HasSuffix(usernamePrefix, "[bot]") {
			username = fmt.Sprintf("%s_%03d[bot]", strings.TrimSuffix(usernamePrefix, "[bot]"), i)
		}
		seedDeveloper(t, db, ctx, username)

		_, err := db.ExecContext(ctx, `
			INSERT INTO devpulse_event (org, repo, username, type, date, url, mentions, labels)
			VALUES ($1, $2, $3, $4, $5, $6, '[]', '[]')
			ON CONFLICT (org, repo, username, type, date) DO NOTHING`,
			org, repo, username, evType, today,
			fmt.Sprintf("https://example.test/%s/%s/%d/%s", org, repo, i, evType))
		require.NoError(t, err)
	}
}

// seedInsights inserts a repo_insights row with the given age in days
// (negative = older) and event_count. generated_at uses the importer's
// "2006-01-02T15:04:05Z" format.
func seedInsights(t *testing.T, db *sql.DB, ctx context.Context, org, repo string, ageDays int, savedEvents int) {
	t.Helper()
	generatedAt := time.Now().UTC().AddDate(0, 0, -ageDays).Format("2006-01-02T15:04:05Z")
	_, err := db.ExecContext(ctx, `
		INSERT INTO devpulse_repo_insights (org, repo, insights_json, period_months, model, generated_at, event_count)
		VALUES ($1, $2, '{}', 3, 'test', $3, $4)
		ON CONFLICT (org, repo) DO UPDATE SET
			generated_at = EXCLUDED.generated_at,
			event_count = EXCLUDED.event_count`,
		org, repo, generatedAt, savedEvents)
	require.NoError(t, err)
}

func findStuck(rows []stuckInsightsRepo, org, repo string) *stuckInsightsRepo {
	for i := range rows {
		if rows[i].Org == org && rows[i].Repo == repo {
			return &rows[i]
		}
	}
	return nil
}

// TestGetStuckInsightsRepos_EmptyDB verifies that a fresh database with no
// data returns an empty slice (not an error).
func TestGetStuckInsightsRepos_EmptyDB(t *testing.T) {
	db := setupE2EDB(t)
	ctx := context.Background()

	rows, err := getStuckInsightsRepos(ctx, db)
	require.NoError(t, err)
	assert.Empty(t, rows)
}

// TestGetStuckInsightsRepos_NoInsightsButHasEvents — a repo with events
// but no insights row qualifies as stuck (HasNoInsights = true).
func TestGetStuckInsightsRepos_NoInsightsButHasEvents(t *testing.T) {
	db := setupE2EDB(t)
	ctx := context.Background()

	seedTenantRepo(t, db, ctx, 1001, "alice", "neverorg", "neverrepo", true)
	seedEvents(t, db, ctx, "neverorg", "neverrepo", "alice", "push", 5)

	rows, err := getStuckInsightsRepos(ctx, db)
	require.NoError(t, err)

	got := findStuck(rows, "neverorg", "neverrepo")
	require.NotNil(t, got, "expected neverorg/neverrepo to be flagged")
	assert.True(t, got.HasNoInsights)
	assert.Equal(t, 0, got.SavedEvents)
	assert.Equal(t, 5, got.CurrentEvents)
}

// TestGetStuckInsightsRepos_FreshInsights — insights generated within the
// past 14 days are never stuck, even with a large delta.
func TestGetStuckInsightsRepos_FreshInsights(t *testing.T) {
	db := setupE2EDB(t)
	ctx := context.Background()

	seedTenantRepo(t, db, ctx, 1002, "bob", "freshorg", "freshrepo", true)
	seedEvents(t, db, ctx, "freshorg", "freshrepo", "bob", "push", 100)
	seedInsights(t, db, ctx, "freshorg", "freshrepo", 3, 10) // 90% delta but age=3d

	rows, err := getStuckInsightsRepos(ctx, db)
	require.NoError(t, err)
	assert.Nil(t, findStuck(rows, "freshorg", "freshrepo"))
}

// TestGetStuckInsightsRepos_OldButSmallDelta — insights older than 14 days
// with <10% change should NOT be flagged (importer's delta gate would skip).
func TestGetStuckInsightsRepos_OldButSmallDelta(t *testing.T) {
	db := setupE2EDB(t)
	ctx := context.Background()

	seedTenantRepo(t, db, ctx, 1003, "carol", "stableorg", "stablerepo", true)
	seedEvents(t, db, ctx, "stableorg", "stablerepo", "carol", "push", 100)
	seedInsights(t, db, ctx, "stableorg", "stablerepo", 30, 95) // 30 days old, 5.3% delta

	rows, err := getStuckInsightsRepos(ctx, db)
	require.NoError(t, err)
	assert.Nil(t, findStuck(rows, "stableorg", "stablerepo"),
		"5%% delta should not flag — importer's gate would skip too")
}

// TestGetStuckInsightsRepos_StuckOldAndChanged — old AND high delta: stuck.
// This is the real stuck-state — both importer gates would pass but no
// regeneration happened.
func TestGetStuckInsightsRepos_StuckOldAndChanged(t *testing.T) {
	db := setupE2EDB(t)
	ctx := context.Background()

	seedTenantRepo(t, db, ctx, 1004, "dave", "stuckorg", "stuckrepo", true)
	seedEvents(t, db, ctx, "stuckorg", "stuckrepo", "dave", "push", 200)
	seedInsights(t, db, ctx, "stuckorg", "stuckrepo", 30, 100) // 30d old, 100% delta

	rows, err := getStuckInsightsRepos(ctx, db)
	require.NoError(t, err)

	got := findStuck(rows, "stuckorg", "stuckrepo")
	require.NotNil(t, got)
	assert.False(t, got.HasNoInsights)
	assert.InDelta(t, 30.0, got.AgeDays, 1.0)
	assert.Equal(t, 100, got.SavedEvents)
	assert.Equal(t, 200, got.CurrentEvents)
	assert.InDelta(t, 100.0, got.DeltaPct, 0.1)
}

// TestGetStuckInsightsRepos_InactiveRepoExcluded — inactive tenant_repo
// entries must not appear in results.
func TestGetStuckInsightsRepos_InactiveRepoExcluded(t *testing.T) {
	db := setupE2EDB(t)
	ctx := context.Background()

	seedTenantRepo(t, db, ctx, 1005, "eve", "inactiveorg", "inactiverepo", false)
	seedEvents(t, db, ctx, "inactiveorg", "inactiverepo", "eve", "push", 50)
	seedInsights(t, db, ctx, "inactiveorg", "inactiverepo", 30, 10) // would qualify

	rows, err := getStuckInsightsRepos(ctx, db)
	require.NoError(t, err)
	assert.Nil(t, findStuck(rows, "inactiveorg", "inactiverepo"))
}

// TestGetStuckInsightsRepos_BotsExcluded — events from bot accounts (username
// ends in "[bot]") must not contribute to current_events, matching the
// importer's GetInsightsSummary filter.
func TestGetStuckInsightsRepos_BotsExcluded(t *testing.T) {
	db := setupE2EDB(t)
	ctx := context.Background()

	seedTenantRepo(t, db, ctx, 1006, "frank", "botorg", "botrepo", true)
	seedEvents(t, db, ctx, "botorg", "botrepo", "dependabot[bot]", "push", 50)
	// No non-bot events => current_events filtered to 0 => not flagged.

	rows, err := getStuckInsightsRepos(ctx, db)
	require.NoError(t, err)
	assert.Nil(t, findStuck(rows, "botorg", "botrepo"))
}

// TestGetStuckInsightsRepos_ForksExcluded — fork events are filtered out,
// matching the importer's selectInsightsSummaryTpl.
func TestGetStuckInsightsRepos_ForksExcluded(t *testing.T) {
	db := setupE2EDB(t)
	ctx := context.Background()

	seedTenantRepo(t, db, ctx, 1007, "grace", "forkorg", "forkrepo", true)
	seedEvents(t, db, ctx, "forkorg", "forkrepo", "grace", "fork", 50)
	// No non-fork events => current_events filtered to 0 => not flagged.

	rows, err := getStuckInsightsRepos(ctx, db)
	require.NoError(t, err)
	assert.Nil(t, findStuck(rows, "forkorg", "forkrepo"))
}

// TestGetStuckInsightsRepos_SortOrder — "never generated" rows surface
// before stale-but-existing rows; among existing rows, older comes first.
func TestGetStuckInsightsRepos_SortOrder(t *testing.T) {
	db := setupE2EDB(t)
	ctx := context.Background()

	// One never-generated, two stale with different ages.
	seedTenantRepo(t, db, ctx, 1008, "h1", "sort", "never", true)
	seedEvents(t, db, ctx, "sort", "never", "h1", "push", 30)

	seedTenantRepo(t, db, ctx, 1009, "h2", "sort", "stale15", true)
	seedEvents(t, db, ctx, "sort", "stale15", "h2", "push", 100)
	seedInsights(t, db, ctx, "sort", "stale15", 15, 50)

	seedTenantRepo(t, db, ctx, 1010, "h3", "sort", "stale60", true)
	seedEvents(t, db, ctx, "sort", "stale60", "h3", "push", 100)
	seedInsights(t, db, ctx, "sort", "stale60", 60, 50)

	rows, err := getStuckInsightsRepos(ctx, db)
	require.NoError(t, err)
	require.Len(t, rows, 3)

	assert.True(t, rows[0].HasNoInsights, "never-generated should sort first")
	assert.Equal(t, "never", rows[0].Repo)
	// Among existing, older first.
	assert.Equal(t, "stale60", rows[1].Repo)
	assert.Equal(t, "stale15", rows[2].Repo)
}

// TestCollectSummary_IncludesStuckInsights — end-to-end check that the new
// field flows through collectSummary into the response.
func TestCollectSummary_IncludesStuckInsights(t *testing.T) {
	db := setupE2EDB(t)
	ctx := context.Background()

	seedTenantRepo(t, db, ctx, 2001, "evan", "summaryorg", "summaryrepo", true)
	seedEvents(t, db, ctx, "summaryorg", "summaryrepo", "evan", "push", 40)
	// No insights row → stuck.

	resp, err := collectSummary(ctx, db)
	require.NoError(t, err)
	require.NotNil(t, findStuck(resp.StuckInsightsRepos, "summaryorg", "summaryrepo"))
}
