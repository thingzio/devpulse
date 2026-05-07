package server

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/thingzio/devpulse/pkg/platformstats"
)

const errorReposSQL = `SELECT org, repo, import_errors, COALESCE(import_last_error, '')
    FROM devpulse_tenant_repo WHERE active = TRUE AND import_errors > 0
    ORDER BY import_errors DESC`

const collectOpsMetricsSQL = `SELECT
    (SELECT COUNT(DISTINCT s.tenant_id) FROM devpulse_session s
     WHERE s.created_at > NOW() - INTERVAL '7 days'),
    (SELECT COUNT(*) FROM devpulse_tenant WHERE tos_accepted_at IS NOT NULL),
    (SELECT COUNT(*) FROM devpulse_tenant WHERE status = 'suspended'),
    (SELECT COUNT(DISTINCT (org, repo)) FROM devpulse_repo_insights),
    (SELECT COUNT(*) FROM devpulse_developer WHERE reputation IS NOT NULL AND username NOT LIKE '%[bot]')`

func collectOpsMetrics(ctx context.Context, db *sql.DB) (opsMetrics, error) {
	var m opsMetrics
	err := db.QueryRowContext(ctx, collectOpsMetricsSQL).Scan(
		&m.ActiveTenants7d, &m.Onboarded, &m.Suspended,
		&m.ReposWithInsights, &m.ScoredContributors,
	)
	if err != nil {
		return m, fmt.Errorf("collecting ops metrics: %w", err)
	}
	return m, nil
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
	current, err := platformstats.Collect(ctx, db)
	if err != nil {
		return summaryResponse{}, fmt.Errorf("collecting stats: %w", err)
	}

	today := time.Now().UTC().Format("2006-01-02")
	if upsertErr := platformstats.Upsert(ctx, db, today, current); upsertErr != nil {
		return summaryResponse{}, fmt.Errorf("upserting stats: %w", upsertErr)
	}

	yesterday := time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02")
	weekAgo := time.Now().UTC().AddDate(0, 0, -7).Format("2006-01-02")
	monthAgo := time.Now().UTC().AddDate(0, 0, -30).Format("2006-01-02")

	prevDay, err := platformstats.Get(ctx, db, yesterday)
	if err != nil {
		return summaryResponse{}, fmt.Errorf("getting yesterday stats: %w", err)
	}
	prevWeek, err := platformstats.Get(ctx, db, weekAgo)
	if err != nil {
		return summaryResponse{}, fmt.Errorf("getting week-ago stats: %w", err)
	}
	prevMonth, err := platformstats.Get(ctx, db, monthAgo)
	if err != nil {
		return summaryResponse{}, fmt.Errorf("getting month-ago stats: %w", err)
	}

	repos, err := getErrorRepos(ctx, db)
	if err != nil {
		return summaryResponse{}, fmt.Errorf("getting error repos: %w", err)
	}

	ops, err := collectOpsMetrics(ctx, db)
	if err != nil {
		return summaryResponse{}, fmt.Errorf("collecting ops metrics: %w", err)
	}

	return summaryResponse{
		Date:       today,
		Current:    current,
		Ops:        ops,
		DoD:        computeDelta(current, prevDay),
		WoW:        computeDelta(current, prevWeek),
		MoM:        computeDelta(current, prevMonth),
		ErrorRepos: repos,
		UpdatedAt:  time.Now().UTC().Format(time.RFC3339),
	}, nil
}
