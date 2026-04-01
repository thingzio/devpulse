package tenant

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// RepoOverview holds repo metadata and activity stats for the dashboard table.
type RepoOverview struct {
	Org          string  `json:"org"`
	Repo         string  `json:"repo"`
	Stars        int     `json:"stars"`
	Forks        int     `json:"forks"`
	OpenIssues   int     `json:"open_issues"`
	Events       int     `json:"events"`
	WeeklyEvents int     `json:"weekly_events"`
	WeeklyPct    float64 `json:"weekly_pct"`
	Contributors int     `json:"contributors"`
	Scored       int     `json:"scored"`
	Language     string  `json:"language"`
	License      string  `json:"license"`
	LastImport   string  `json:"last_import"`
	LimitReached bool    `json:"limit_reached"`
}

// UsageSummary holds tenant-level usage stats for the dashboard.
type UsageSummary struct {
	Plan             string  `json:"plan"`
	MaxRepos         int     `json:"max_repos"`
	ActiveRepos      int     `json:"active_repos"`
	MaxEventsPerWeek int     `json:"max_events_per_week"`
	WeeklyEvents     int     `json:"weekly_events"`
	WeeklyPct        float64 `json:"weekly_pct"`
	LimitReached     bool    `json:"limit_reached"`
}

// OverviewResponse combines repo overview and usage summary.
type OverviewResponse struct {
	Usage UsageSummary   `json:"usage"`
	Repos []RepoOverview `json:"repos"`
}

const tenantRepoOverviewSQL = `
	SELECT tr.org, tr.repo,
		COALESCE(rm.stars, 0),
		COALESCE(rm.forks, 0),
		COALESCE(rm.open_issues, 0),
		COUNT(e.type),
		COUNT(CASE WHEN e.date >= $3 THEN 1 END),
		COUNT(DISTINCT e.username),
		COUNT(DISTINCT CASE WHEN d.reputation IS NOT NULL THEN e.username END),
		COALESCE(rm.language, ''),
		COALESCE(rm.license, ''),
		COALESCE(rm.last_import_at, '')
	FROM tenant_repo tr
	LEFT JOIN repo_meta rm ON rm.org = tr.org AND rm.repo = tr.repo
	LEFT JOIN event e ON tr.org = e.org AND tr.repo = e.repo AND e.date >= $2
	LEFT JOIN developer d ON e.username = d.username
	WHERE tr.tenant_id = $1 AND tr.active = TRUE
	GROUP BY tr.org, tr.repo, rm.stars, rm.forks, rm.open_issues,
		rm.language, rm.license, rm.last_import_at
	ORDER BY tr.org, tr.repo`

const tenantLimitsSQL = `
	SELECT plan, max_repos, max_events_per_week
	FROM tenant WHERE id = $1`

const tenantActiveRepoCountSQL = `
	SELECT COUNT(*) FROM tenant_repo WHERE tenant_id = $1 AND active = TRUE`

// GetOverview returns the full overview response with repo data and usage summary.
func GetOverview(ctx context.Context, db *sql.DB, tenantID string, months int) (*OverviewResponse, error) {
	since := time.Now().UTC().AddDate(0, -months, 0).Format("2006-01-02")
	weekStart := startOfWeek().Format("2006-01-02")

	// Get tenant limits
	var plan string
	var maxRepos, maxEventsPerWeek int
	if err := db.QueryRowContext(ctx, tenantLimitsSQL, tenantID).Scan(&plan, &maxRepos, &maxEventsPerWeek); err != nil {
		return nil, fmt.Errorf("querying tenant limits: %w", err)
	}

	// Get active repo count
	var activeRepos int
	if err := db.QueryRowContext(ctx, tenantActiveRepoCountSQL, tenantID).Scan(&activeRepos); err != nil {
		return nil, fmt.Errorf("querying active repos: %w", err)
	}

	// Get repo overview with weekly events
	rows, err := db.QueryContext(ctx, tenantRepoOverviewSQL, tenantID, since, weekStart)
	if err != nil {
		return nil, fmt.Errorf("querying tenant repo overview: %w", err)
	}
	defer rows.Close()

	var repos []RepoOverview
	var totalWeeklyEvents int
	for rows.Next() {
		var r RepoOverview
		if err := rows.Scan(&r.Org, &r.Repo, &r.Stars, &r.Forks, &r.OpenIssues,
			&r.Events, &r.WeeklyEvents,
			&r.Contributors, &r.Scored,
			&r.Language, &r.License, &r.LastImport); err != nil {
			return nil, fmt.Errorf("scanning repo overview: %w", err)
		}
		totalWeeklyEvents += r.WeeklyEvents
		repos = append(repos, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating repo overview: %w", err)
	}

	// Compute percentages and limit flags
	limitReached := totalWeeklyEvents >= maxEventsPerWeek
	for i := range repos {
		if maxEventsPerWeek > 0 {
			repos[i].WeeklyPct = float64(repos[i].WeeklyEvents) / float64(maxEventsPerWeek) * 100
		}
		repos[i].LimitReached = limitReached
	}

	weeklyPct := float64(0)
	if maxEventsPerWeek > 0 {
		weeklyPct = float64(totalWeeklyEvents) / float64(maxEventsPerWeek) * 100
	}

	return &OverviewResponse{
		Usage: UsageSummary{
			Plan:             plan,
			MaxRepos:         maxRepos,
			ActiveRepos:      activeRepos,
			MaxEventsPerWeek: maxEventsPerWeek,
			WeeklyEvents:     totalWeeklyEvents,
			WeeklyPct:        weeklyPct,
			LimitReached:     limitReached,
		},
		Repos: repos,
	}, nil
}

// GetWeeklyEventCount returns the total events this week for a tenant's repos.
func GetWeeklyEventCount(ctx context.Context, db *sql.DB, tenantID string) (int, error) {
	weekStart := startOfWeek().Format("2006-01-02")
	var count int
	err := db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM event e
		JOIN tenant_repo tr ON tr.org = e.org AND tr.repo = e.repo
		WHERE tr.tenant_id = $1 AND tr.active = TRUE AND e.date >= $2`,
		tenantID, weekStart).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("querying weekly events: %w", err)
	}
	return count, nil
}

func GetActiveRepoCount(ctx context.Context, db *sql.DB, tenantID string) (int, error) {
	var count int
	if err := db.QueryRowContext(ctx, tenantActiveRepoCountSQL, tenantID).Scan(&count); err != nil {
		return 0, fmt.Errorf("counting active repos: %w", err)
	}
	return count, nil
}

func startOfWeek() time.Time {
	return startOfWeekFrom(time.Now().UTC())
}

func startOfWeekFrom(t time.Time) time.Time {
	weekday := int(t.Weekday())
	if weekday == 0 {
		weekday = 7
	}
	return t.AddDate(0, 0, -(weekday - 1)).Truncate(24 * time.Hour)
}
