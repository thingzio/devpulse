package tenant

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/thingzio/devpulse/pkg/data"
)

// TenantSummary is a lightweight tenant record for admin listing.
type TenantSummary struct {
	Username         string
	Plan             string
	MaxRepos         int
	MaxEventsPerWeek int
	CreatedAt        time.Time
	LastSignIn       *time.Time
}

// TenantDetail is a full tenant record for admin inspection.
type TenantDetail struct {
	ID               string
	Username         string
	Email            string
	Plan             string
	MaxRepos         int
	MaxEventsPerWeek int
	CreatedAt        time.Time
	LastSignIn       *time.Time
}

// RepoDetail holds per-repo statistics for admin inspection.
type RepoDetail struct {
	Org            string
	Repo           string
	Events         int
	WeeklyEvents   int
	LastImport     string
	PRTotal        int
	PRMissingSize  int
	Contributors   int
	Scored         int
	DeepScored     int
	NeverDeepScore int
}

const (
	selectTenantIDByUsernameSQL = `SELECT id FROM tenant WHERE username = $1`

	clearUpgradeRequestSQL = `
		UPDATE tenant SET upgrade_requested_at = NULL, updated_at = NOW()
		WHERE id = $1`

	insertMinimalTenantSQL = `
		INSERT INTO tenant (github_id, username)
		VALUES ($1, $2)
		ON CONFLICT (github_id) DO UPDATE SET updated_at = NOW()
		RETURNING id`

	listTenantSummariesSQL = `
		SELECT t.username, t.plan, t.max_repos, t.max_events_per_week,
		       t.created_at, MAX(s.created_at) AS last_sign_in
		FROM tenant t
		LEFT JOIN session s ON s.tenant_id = t.id
		GROUP BY t.id
		ORDER BY t.created_at`

	getTenantDetailByUsernameSQL = `
		SELECT t.id, t.username, t.email, t.plan,
		       t.max_repos, t.max_events_per_week,
		       t.created_at, MAX(s.created_at) AS last_sign_in
		FROM tenant t
		LEFT JOIN session s ON s.tenant_id = t.id
		WHERE t.username = $1
		GROUP BY t.id`

	// getTenantRepoDetailsSQL: $1=tenantID, $2=eventsSince, $3=weekStart, $4=backfillSince
	getTenantRepoDetailsSQL = `
		SELECT tr.org, tr.repo,
		       COUNT(e.type),
		       COUNT(CASE WHEN e.date >= $3 THEN 1 END),
		       COALESCE(rm.last_import_at, ''),
		       COUNT(CASE WHEN e.type = 'pr' AND e.number IS NOT NULL AND e.number > 0
		                   AND e.created_at::date >= $4 THEN 1 END),
		       COUNT(CASE WHEN e.type = 'pr' AND e.number IS NOT NULL AND e.number > 0
		                   AND e.created_at::date >= $4
		                   AND (e.additions IS NULL OR e.changed_files IS NULL) THEN 1 END),
		       COUNT(DISTINCT CASE WHEN ` + data.ContribExcludeSQL + ` THEN e.username END),
		       COUNT(DISTINCT CASE WHEN ` + data.ContribExcludeSQL + `
		                   AND d.reputation IS NOT NULL THEN e.username END),
		       COUNT(DISTINCT CASE WHEN ` + data.ContribExcludeSQL + `
		                   AND d.reputation_deep = 1 THEN e.username END),
		       COUNT(DISTINCT CASE WHEN ` + data.ContribExcludeSQL + `
		                   AND (d.reputation IS NULL OR d.reputation_deep IS NULL OR d.reputation_deep = 0) THEN e.username END)
		FROM tenant_repo tr
		LEFT JOIN repo_meta rm ON rm.org = tr.org AND rm.repo = tr.repo
		LEFT JOIN event e ON tr.org = e.org AND tr.repo = e.repo
		       AND e.date >= $2
		LEFT JOIN developer d ON e.username = d.username
		WHERE tr.tenant_id = $1 AND tr.active = TRUE
		GROUP BY tr.org, tr.repo, rm.last_import_at
		ORDER BY tr.org, tr.repo`
)

// GetTenantIDByUsername returns the tenant ID for a given GitHub username.
func GetTenantIDByUsername(ctx context.Context, db *sql.DB, username string) (string, error) {
	var id string
	if err := db.QueryRowContext(ctx, selectTenantIDByUsernameSQL, username).Scan(&id); err != nil {
		return "", fmt.Errorf("getting tenant by username %q: %w", username, err)
	}
	return id, nil
}

// ClearUpgradeRequest removes a pending upgrade request for a tenant.
func ClearUpgradeRequest(ctx context.Context, db *sql.DB, tenantID string) error {
	if _, err := db.ExecContext(ctx, clearUpgradeRequestSQL, tenantID); err != nil {
		return fmt.Errorf("clearing upgrade request: %w", err)
	}
	return nil
}

// InsertMinimalTenant creates a tenant with only GitHub ID and username (for admin invites).
func InsertMinimalTenant(ctx context.Context, db *sql.DB, githubID int64, username string) (string, error) {
	var id string
	if err := db.QueryRowContext(ctx, insertMinimalTenantSQL, githubID, username).Scan(&id); err != nil {
		return "", fmt.Errorf("inserting minimal tenant %q: %w", username, err)
	}
	return id, nil
}

// ListTenantSummaries returns all tenants with their last sign-in time.
func ListTenantSummaries(ctx context.Context, db *sql.DB) ([]TenantSummary, error) {
	rows, err := db.QueryContext(ctx, listTenantSummariesSQL)
	if err != nil {
		return nil, fmt.Errorf("listing tenant summaries: %w", err)
	}
	defer rows.Close()

	var tenants []TenantSummary
	for rows.Next() {
		var t TenantSummary
		var lastSignIn sql.NullTime
		if err := rows.Scan(
			&t.Username, &t.Plan, &t.MaxRepos, &t.MaxEventsPerWeek,
			&t.CreatedAt, &lastSignIn,
		); err != nil {
			return nil, fmt.Errorf("scanning tenant summary: %w", err)
		}
		if lastSignIn.Valid {
			t.LastSignIn = &lastSignIn.Time
		}
		tenants = append(tenants, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating tenant summaries: %w", err)
	}
	return tenants, nil
}

// GetTenantDetailByUsername returns detailed tenant info including email and last sign-in.
func GetTenantDetailByUsername(ctx context.Context, db *sql.DB, username string) (*TenantDetail, error) {
	var td TenantDetail
	var lastSignIn sql.NullTime
	var email sql.NullString
	err := db.QueryRowContext(ctx, getTenantDetailByUsernameSQL, username).Scan(
		&td.ID, &td.Username, &email, &td.Plan,
		&td.MaxRepos, &td.MaxEventsPerWeek,
		&td.CreatedAt, &lastSignIn,
	)
	if err != nil {
		return nil, fmt.Errorf("getting tenant detail for %q: %w", username, err)
	}
	if lastSignIn.Valid {
		td.LastSignIn = &lastSignIn.Time
	}
	if email.Valid {
		td.Email = email.String
	}
	return &td, nil
}

// GetTenantRepoDetails returns per-repo statistics for a tenant.
// backfillSince scopes PR backfill counts to match the worker's BACKFILL_MAX_DAYS window.
func GetTenantRepoDetails(ctx context.Context, db *sql.DB, tenantID, since, weekStart, backfillSince string) ([]RepoDetail, error) {
	rows, err := db.QueryContext(ctx, getTenantRepoDetailsSQL, tenantID, since, weekStart, backfillSince)
	if err != nil {
		return nil, fmt.Errorf("querying tenant repo details: %w", err)
	}
	defer rows.Close()

	var repos []RepoDetail
	for rows.Next() {
		var rd RepoDetail
		if err := rows.Scan(
			&rd.Org, &rd.Repo, &rd.Events, &rd.WeeklyEvents, &rd.LastImport,
			&rd.PRTotal, &rd.PRMissingSize,
			&rd.Contributors, &rd.Scored, &rd.DeepScored, &rd.NeverDeepScore,
		); err != nil {
			return nil, fmt.Errorf("scanning repo detail: %w", err)
		}
		repos = append(repos, rd)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating repo details: %w", err)
	}
	return repos, nil
}
