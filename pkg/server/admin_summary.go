package server

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

const errorReposSQL = `SELECT org, repo, import_errors, COALESCE(import_last_error, '')
    FROM devpulse_tenant_repo WHERE active = TRUE AND import_errors > 0
    ORDER BY import_errors DESC`

func collectStats(ctx context.Context, db *sql.DB) (platformStats, error) {
	var s platformStats
	err := db.QueryRowContext(ctx, collectStatsSQL).Scan(
		&s.Tenants, &s.TenantsFree, &s.TenantsStarter, &s.TenantsPro, &s.TenantsEnterprise,
		&s.Repos, &s.Events, &s.Contributors, &s.Installations, &s.ReposWithErrors,
	)
	if err != nil {
		return s, fmt.Errorf("collecting platform stats: %w", err)
	}
	return s, nil
}

func upsertStats(ctx context.Context, db *sql.DB, date string, s platformStats) error {
	_, err := db.ExecContext(ctx, upsertStatsSQL,
		date, s.Tenants, s.TenantsFree, s.TenantsStarter, s.TenantsPro, s.TenantsEnterprise,
		s.Repos, s.Events, s.Contributors, s.Installations, s.ReposWithErrors,
	)
	if err != nil {
		return fmt.Errorf("upserting platform stats: %w", err)
	}
	return nil
}

func getStats(ctx context.Context, db *sql.DB, date string) (*platformStats, error) {
	var s platformStats
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

func getErrorRepos(ctx context.Context, db *sql.DB) ([]errorRepo, error) {
	rows, err := db.QueryContext(ctx, errorReposSQL)
	if err != nil {
		return nil, fmt.Errorf("querying error repos: %w", err)
	}
	defer rows.Close()

	var out []errorRepo
	for rows.Next() {
		var r errorRepo
		if err := rows.Scan(&r.Org, &r.Repo, &r.Errors, &r.LastError); err != nil {
			return nil, fmt.Errorf("scanning error repo: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating error repos: %w", err)
	}
	return out, nil
}

func computeDelta(current platformStats, prev *platformStats) *statsDelta {
	if prev == nil {
		return nil
	}

	intDelta := func(cur, old int) *int {
		v := cur - old
		return &v
	}
	int64Delta := func(cur, old int64) *int64 {
		v := cur - old
		return &v
	}
	pct := func(cur, old int) *float64 {
		if old == 0 {
			return nil
		}
		v := float64(cur-old) / float64(old) * 100
		return &v
	}
	pct64 := func(cur, old int64) *float64 {
		if old == 0 {
			return nil
		}
		v := float64(cur-old) / float64(old) * 100
		return &v
	}

	return &statsDelta{
		Tenants:         intDelta(current.Tenants, prev.Tenants),
		TenantsPct:      pct(current.Tenants, prev.Tenants),
		Repos:           intDelta(current.Repos, prev.Repos),
		ReposPct:        pct(current.Repos, prev.Repos),
		Events:          int64Delta(current.Events, prev.Events),
		EventsPct:       pct64(current.Events, prev.Events),
		Contributors:    intDelta(current.Contributors, prev.Contributors),
		ContribPct:      pct(current.Contributors, prev.Contributors),
		Installations:   intDelta(current.Installations, prev.Installations),
		InstallPct:      pct(current.Installations, prev.Installations),
		ReposWithErrors: intDelta(current.ReposWithErrors, prev.ReposWithErrors),
		ErrorsPct:       pct(current.ReposWithErrors, prev.ReposWithErrors),
	}
}

func collectSummary(ctx context.Context, db *sql.DB) (summaryResponse, error) {
	current, err := collectStats(ctx, db)
	if err != nil {
		return summaryResponse{}, fmt.Errorf("collecting stats: %w", err)
	}

	today := time.Now().UTC().Format("2006-01-02")
	if upsertErr := upsertStats(ctx, db, today, current); upsertErr != nil {
		return summaryResponse{}, fmt.Errorf("upserting stats: %w", upsertErr)
	}

	yesterday := time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02")
	weekAgo := time.Now().UTC().AddDate(0, 0, -7).Format("2006-01-02")
	monthAgo := time.Now().UTC().AddDate(0, 0, -30).Format("2006-01-02")

	prevDay, err := getStats(ctx, db, yesterday)
	if err != nil {
		return summaryResponse{}, fmt.Errorf("getting yesterday stats: %w", err)
	}
	prevWeek, err := getStats(ctx, db, weekAgo)
	if err != nil {
		return summaryResponse{}, fmt.Errorf("getting week-ago stats: %w", err)
	}
	prevMonth, err := getStats(ctx, db, monthAgo)
	if err != nil {
		return summaryResponse{}, fmt.Errorf("getting month-ago stats: %w", err)
	}

	repos, err := getErrorRepos(ctx, db)
	if err != nil {
		return summaryResponse{}, fmt.Errorf("getting error repos: %w", err)
	}

	return summaryResponse{
		Date:       today,
		Current:    current,
		DoD:        computeDelta(current, prevDay),
		WoW:        computeDelta(current, prevWeek),
		MoM:        computeDelta(current, prevMonth),
		ErrorRepos: repos,
		UpdatedAt:  time.Now().UTC().Format(time.RFC3339),
	}, nil
}
