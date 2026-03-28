package tenant

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// RepoOverview holds repo metadata and activity stats for the dashboard table.
type RepoOverview struct {
	Org          string `json:"org"`
	Repo         string `json:"repo"`
	Stars        int    `json:"stars"`
	Forks        int    `json:"forks"`
	OpenIssues   int    `json:"open_issues"`
	Events       int    `json:"events"`
	Contributors int    `json:"contributors"`
	Scored       int    `json:"scored"`
	Language     string `json:"language"`
	License      string `json:"license"`
	LastImport   string `json:"last_import"`
}

const tenantRepoOverviewSQL = `
	SELECT tr.org, tr.repo,
		COALESCE(rm.stars, 0),
		COALESCE(rm.forks, 0),
		COALESCE(rm.open_issues, 0),
		COUNT(e.type),
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

// GetRepoOverview returns repo overview data scoped to a tenant's tracked repos.
func GetRepoOverview(ctx context.Context, db *sql.DB, tenantID string, months int) ([]RepoOverview, error) {
	since := time.Now().UTC().AddDate(0, -months, 0).Format("2006-01-02")

	rows, err := db.QueryContext(ctx, tenantRepoOverviewSQL, tenantID, since)
	if err != nil {
		return nil, fmt.Errorf("querying tenant repo overview: %w", err)
	}
	defer rows.Close()

	var result []RepoOverview
	for rows.Next() {
		var r RepoOverview
		if err := rows.Scan(&r.Org, &r.Repo, &r.Stars, &r.Forks, &r.OpenIssues,
			&r.Events, &r.Contributors, &r.Scored,
			&r.Language, &r.License, &r.LastImport); err != nil {
			return nil, fmt.Errorf("scanning repo overview: %w", err)
		}
		result = append(result, r)
	}
	return result, rows.Err()
}
