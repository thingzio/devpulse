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

// Stuck-insights detection: a repo should have regenerated insights if both
// importer gates would pass (insights age > 7 days AND event delta > 10%).
// We use a more conservative 14-day age threshold so day 8–13 quiet repos
// — which are eligible but normally regenerate on the next nightly run —
// don't appear here. The 63-day window and 10% delta match the importer
// constants in pkg/importer/importer.go (insightsPeriodWeeks = 9,
// insightsEventDeltaPct = 0.10).
const (
	stuckInsightsWindowDays = 63
	stuckInsightsAgeDays    = 14
	stuckInsightsDeltaPct   = 10.0
	stuckInsightsLimit      = 50
)

// stuckInsightsReposSQL finds repos that should have regenerated insights
// (importer gates would pass) but haven't. The CTE structure mirrors the
// importer's filters: non-bot, non-fork events joined to devpulse_developer.
// $1 = since date (today - 63 days), $2 = age days threshold,
// $3 = delta percent threshold, $4 = LIMIT.
const stuckInsightsReposSQL = `WITH active_repos AS (
    SELECT DISTINCT tr.org, tr.repo
    FROM devpulse_tenant_repo tr
    WHERE tr.active = TRUE
),
event_counts AS (
    SELECT e.org, e.repo, COUNT(*) AS current_events
    FROM devpulse_event e
    JOIN devpulse_developer d ON e.username = d.username
    WHERE e.date >= $1
      AND e.username NOT LIKE '%[bot]'
      AND e.type != 'fork'
    GROUP BY e.org, e.repo
),
joined AS (
    SELECT
        ar.org,
        ar.repo,
        COALESCE(ec.current_events, 0) AS current_events,
        COALESCE(ri.event_count, 0) AS saved_events,
        COALESCE(TO_CHAR(ri.generated_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'), '') AS generated_at,
        CASE
            WHEN ri.generated_at IS NULL THEN NULL
            ELSE EXTRACT(EPOCH FROM (NOW() - ri.generated_at)) / 86400.0
        END AS age_days,
        ri.generated_at IS NULL AS has_no_insights
    FROM active_repos ar
    LEFT JOIN event_counts ec ON ec.org = ar.org AND ec.repo = ar.repo
    LEFT JOIN devpulse_repo_insights ri ON ri.org = ar.org AND ri.repo = ar.repo
)
SELECT
    org, repo, generated_at,
    COALESCE(age_days, 0),
    saved_events, current_events,
    CASE
        WHEN saved_events = 0 THEN 100.0
        ELSE ABS(current_events - saved_events) * 100.0 / saved_events
    END AS delta_pct,
    has_no_insights
FROM joined
WHERE current_events > 0
  AND (age_days IS NULL OR age_days > $2)
  AND (
      saved_events = 0
      OR ABS(current_events - saved_events) * 100.0 / saved_events > $3
  )
ORDER BY has_no_insights DESC, age_days DESC NULLS FIRST,
         (CASE WHEN saved_events = 0 THEN 100.0
               ELSE ABS(current_events - saved_events) * 100.0 / saved_events
          END) DESC
LIMIT $4`

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

func getStuckInsightsRepos(ctx context.Context, db *sql.DB) ([]stuckInsightsRepo, error) {
	since := time.Now().UTC().AddDate(0, 0, -stuckInsightsWindowDays).Format("2006-01-02")
	rows, err := db.QueryContext(ctx, stuckInsightsReposSQL,
		since, stuckInsightsAgeDays, stuckInsightsDeltaPct, stuckInsightsLimit)
	if err != nil {
		return nil, fmt.Errorf("querying stuck insights repos: %w", err)
	}
	defer rows.Close()

	var out []stuckInsightsRepo
	for rows.Next() {
		var r stuckInsightsRepo
		if err := rows.Scan(&r.Org, &r.Repo, &r.GeneratedAt, &r.AgeDays,
			&r.SavedEvents, &r.CurrentEvents, &r.DeltaPct, &r.HasNoInsights); err != nil {
			return nil, fmt.Errorf("scanning stuck insights repo: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating stuck insights repos: %w", err)
	}
	return out, nil
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

	stuck, err := getStuckInsightsRepos(ctx, db)
	if err != nil {
		return summaryResponse{}, fmt.Errorf("getting stuck insights repos: %w", err)
	}

	ops, err := collectOpsMetrics(ctx, db)
	if err != nil {
		return summaryResponse{}, fmt.Errorf("collecting ops metrics: %w", err)
	}

	return summaryResponse{
		Date:               today,
		Current:            current,
		Ops:                ops,
		DoD:                computeDelta(current, prevDay),
		WoW:                computeDelta(current, prevWeek),
		MoM:                computeDelta(current, prevMonth),
		ErrorRepos:         repos,
		StuckInsightsRepos: stuck,
		UpdatedAt:          time.Now().UTC().Format(time.RFC3339),
	}, nil
}
