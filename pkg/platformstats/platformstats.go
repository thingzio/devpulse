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

// Package platformstats writes daily admin-dashboard rollups to
// devpulse_platform_stats. Both the admin handler and the import job
// snapshot stats so DoD/WoW/MoM deltas remain meaningful even on days
// no admin visits the dashboard.
package platformstats

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

const collectStatsSQL = `SELECT
    (SELECT COUNT(*) FROM devpulse_tenant),
    (SELECT COUNT(*) FROM devpulse_tenant WHERE plan = 'free'),
    (SELECT COUNT(*) FROM devpulse_tenant WHERE plan = 'starter'),
    (SELECT COUNT(*) FROM devpulse_tenant WHERE plan = 'pro'),
    (SELECT COUNT(*) FROM devpulse_tenant WHERE plan = 'enterprise'),
    (SELECT COUNT(*) FROM devpulse_tenant_repo WHERE active = TRUE),
    (SELECT COUNT(*) FROM devpulse_event),
    (SELECT COUNT(DISTINCT username) FROM devpulse_developer WHERE username NOT LIKE '%[bot]'),
    (SELECT COUNT(*) FROM devpulse_github_app_installation WHERE suspended_at IS NULL),
    (SELECT COUNT(DISTINCT id) FROM devpulse_tenant_repo WHERE active = TRUE AND import_errors > 0)`

const upsertStatsSQL = `INSERT INTO devpulse_platform_stats
    (date, tenants, tenants_free, tenants_starter, tenants_pro, tenants_enterprise,
     repos, events, contributors, installations, repos_with_errors, updated_at)
    VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, NOW())
    ON CONFLICT (date) DO UPDATE SET
        tenants = EXCLUDED.tenants, tenants_free = EXCLUDED.tenants_free,
        tenants_starter = EXCLUDED.tenants_starter, tenants_pro = EXCLUDED.tenants_pro,
        tenants_enterprise = EXCLUDED.tenants_enterprise, repos = EXCLUDED.repos,
        events = EXCLUDED.events, contributors = EXCLUDED.contributors,
        installations = EXCLUDED.installations, repos_with_errors = EXCLUDED.repos_with_errors,
        updated_at = NOW()`

const getStatsSQL = `SELECT tenants, tenants_free, tenants_starter, tenants_pro, tenants_enterprise,
    repos, events, contributors, installations, repos_with_errors
    FROM devpulse_platform_stats WHERE date = $1`

// Stats is a single daily rollup row in devpulse_platform_stats.
type Stats struct {
	Tenants           int   `json:"tenants"`
	TenantsFree       int   `json:"tenants_free"`
	TenantsStarter    int   `json:"tenants_starter"`
	TenantsPro        int   `json:"tenants_pro"`
	TenantsEnterprise int   `json:"tenants_enterprise"`
	Repos             int   `json:"repos"`
	Events            int64 `json:"events"`
	Contributors      int   `json:"contributors"`
	Installations     int   `json:"installations"`
	ReposWithErrors   int   `json:"repos_with_errors"`
}

// Collect runs the live aggregate query and returns current stats.
func Collect(ctx context.Context, db *sql.DB) (Stats, error) {
	var s Stats
	err := db.QueryRowContext(ctx, collectStatsSQL).Scan(
		&s.Tenants, &s.TenantsFree, &s.TenantsStarter, &s.TenantsPro, &s.TenantsEnterprise,
		&s.Repos, &s.Events, &s.Contributors, &s.Installations, &s.ReposWithErrors,
	)
	if err != nil {
		return s, fmt.Errorf("collecting platform stats: %w", err)
	}
	return s, nil
}

// Upsert writes stats for date (idempotent — primary key on date).
func Upsert(ctx context.Context, db *sql.DB, date string, s Stats) error {
	_, err := db.ExecContext(ctx, upsertStatsSQL,
		date, s.Tenants, s.TenantsFree, s.TenantsStarter, s.TenantsPro, s.TenantsEnterprise,
		s.Repos, s.Events, s.Contributors, s.Installations, s.ReposWithErrors,
	)
	if err != nil {
		return fmt.Errorf("upserting platform stats: %w", err)
	}
	return nil
}

// Get returns the stats row for date, or (nil, nil) if no row exists.
func Get(ctx context.Context, db *sql.DB, date string) (*Stats, error) {
	var s Stats
	err := db.QueryRowContext(ctx, getStatsSQL, date).Scan(
		&s.Tenants, &s.TenantsFree, &s.TenantsStarter, &s.TenantsPro, &s.TenantsEnterprise,
		&s.Repos, &s.Events, &s.Contributors, &s.Installations, &s.ReposWithErrors,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("getting stats for %s: %w", date, err)
	}
	return &s, nil
}

// Snapshot collects current stats and upserts them under today's UTC date.
// Safe to call from many places — primary key on date makes it idempotent.
func Snapshot(ctx context.Context, db *sql.DB) error {
	s, err := Collect(ctx, db)
	if err != nil {
		return err
	}
	today := time.Now().UTC().Format("2006-01-02")
	return Upsert(ctx, db, today, s)
}
