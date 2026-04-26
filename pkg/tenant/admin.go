package tenant

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/thingzio/devpulse/pkg/data"
)

// adminConn acquires a dedicated DB connection and explicitly clears
// app.tenant_id so the RLS "bypass when no tenant" policy fires. Without
// this, admin queries against the shared pool can pick up a connection
// with a stale tenant scope set by an earlier scoped data API request,
// silently returning a tenant-filtered subset of rows.
//
// Callers MUST close the returned connection. Errors from the cleanup
// path are returned so calling code fails closed instead of running a
// query that may be RLS-filtered.
func adminConn(ctx context.Context, db *sql.DB) (*sql.Conn, error) {
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquiring admin connection: %w", err)
	}
	if _, err := conn.ExecContext(ctx, "SELECT set_config('app.tenant_id', '', false)"); err != nil {
		conn.Close()
		return nil, fmt.Errorf("clearing tenant scope on admin connection: %w", err)
	}
	return conn, nil
}

// TenantSummary is a lightweight tenant record for admin listing.
type TenantSummary struct {
	Username         string
	Email            string
	Name             string
	Plan             string
	Status           string
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
	Name             string
	Company          string
	Location         string
	Bio              string
	Plan             string
	Status           string
	MaxRepos         int
	MaxEventsPerWeek int
	CreatedAt        time.Time
	LastSignIn       *time.Time
	DigestLastSentAt *time.Time
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
	BackfillDays   int // days of backfill coverage (0 = not started)
	BackfillTarget int // target days (EventAgeDaysDefault)
}

const selectMinBackfillSQL = `
	SELECT MIN(backfill_until) FROM devpulse_state
	WHERE org = $1 AND repo = $2 AND backfill_until IS NOT NULL`

const (
	selectTenantIDByUsernameSQL = `SELECT id FROM devpulse_tenant WHERE username = $1`

	clearUpgradeRequestSQL = `
		UPDATE devpulse_tenant SET upgrade_requested_at = NULL, updated_at = NOW()
		WHERE id = $1`

	insertMinimalTenantSQL = `
		INSERT INTO devpulse_tenant (github_id, username)
		VALUES ($1, $2)
		ON CONFLICT (github_id) DO UPDATE SET updated_at = NOW()
		RETURNING id`

	listTenantSummariesSQL = `
		SELECT t.username, COALESCE(t.email, ''), COALESCE(t.name, ''),
		       t.plan, t.status, t.max_repos, t.max_events_per_week,
		       t.created_at, MAX(s.created_at) AS last_sign_in
		FROM devpulse_tenant t
		LEFT JOIN devpulse_session s ON s.tenant_id = t.id
		GROUP BY t.id
		ORDER BY t.created_at`

	countTenantSummariesSearchSQL = `
		SELECT COUNT(*) FROM devpulse_tenant t
		WHERE ($1 = '' OR t.username ILIKE '%' || $1 || '%' OR COALESCE(t.name, '') ILIKE '%' || $1 || '%')`

	listTenantSummariesPagedSQL = `
		SELECT t.username, COALESCE(t.email, ''), COALESCE(t.name, ''),
		       t.plan, t.status, t.max_repos, t.max_events_per_week,
		       t.created_at, MAX(s.created_at) AS last_sign_in
		FROM devpulse_tenant t
		LEFT JOIN devpulse_session s ON s.tenant_id = t.id
		WHERE ($1 = '' OR t.username ILIKE '%' || $1 || '%' OR COALESCE(t.name, '') ILIKE '%' || $1 || '%')
		GROUP BY t.id
		ORDER BY MAX(s.created_at) DESC NULLS LAST
		LIMIT $2 OFFSET $3`

	getTenantDetailByUsernameSQL = `
		SELECT t.id, t.username, t.email,
		       COALESCE(t.name, ''), COALESCE(t.company, ''), COALESCE(t.location, ''), COALESCE(t.bio, ''),
		       t.plan, t.status, t.max_repos, t.max_events_per_week,
		       t.created_at, MAX(s.created_at) AS last_sign_in,
		       t.digest_last_sent_at
		FROM devpulse_tenant t
		LEFT JOIN devpulse_session s ON s.tenant_id = t.id
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
		                   AND d.reputation IS NOT NULL THEN e.username END)
		FROM devpulse_tenant_repo tr
		LEFT JOIN devpulse_repo_meta rm ON rm.org = tr.org AND rm.repo = tr.repo
		LEFT JOIN devpulse_event e ON tr.org = e.org AND tr.repo = e.repo
		       AND e.date >= $2
		LEFT JOIN devpulse_developer d ON e.username = d.username
		WHERE tr.tenant_id = $1 AND tr.active = TRUE
		GROUP BY tr.org, tr.repo, rm.last_import_at
		ORDER BY tr.org, tr.repo`
)

// GetTenantIDByUsername returns the tenant ID for a given GitHub username.
func GetTenantIDByUsername(ctx context.Context, db *sql.DB, username string) (string, error) {
	conn, err := adminConn(ctx, db)
	if err != nil {
		return "", err
	}
	defer conn.Close()

	var id string
	if err := conn.QueryRowContext(ctx, selectTenantIDByUsernameSQL, username).Scan(&id); err != nil {
		return "", fmt.Errorf("getting tenant by username %q: %w", username, err)
	}
	return id, nil
}

// ClearUpgradeRequest removes a pending upgrade request for a tenant.
func ClearUpgradeRequest(ctx context.Context, db *sql.DB, tenantID string) error {
	conn, err := adminConn(ctx, db)
	if err != nil {
		return err
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, clearUpgradeRequestSQL, tenantID); err != nil {
		return fmt.Errorf("clearing upgrade request: %w", err)
	}
	return nil
}

// InsertMinimalTenant creates a tenant with only GitHub ID and username (for admin invites).
func InsertMinimalTenant(ctx context.Context, db *sql.DB, githubID int64, username string) (string, error) {
	conn, err := adminConn(ctx, db)
	if err != nil {
		return "", err
	}
	defer conn.Close()

	var id string
	if err := conn.QueryRowContext(ctx, insertMinimalTenantSQL, githubID, username).Scan(&id); err != nil {
		return "", fmt.Errorf("inserting minimal tenant %q: %w", username, err)
	}
	return id, nil
}

// TenantSummaryPage is a paginated result of tenant summaries.
type TenantSummaryPage struct {
	Tenants []TenantSummary
	Total   int
}

// ListTenantSummariesPaged returns tenants filtered by search, sorted by last sign-in DESC, with pagination.
func ListTenantSummariesPaged(ctx context.Context, db *sql.DB, search string, limit, offset int) (*TenantSummaryPage, error) {
	conn, err := adminConn(ctx, db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	var total int
	if cErr := conn.QueryRowContext(ctx, countTenantSummariesSearchSQL, search).Scan(&total); cErr != nil {
		return nil, fmt.Errorf("counting tenant summaries: %w", cErr)
	}

	rows, err := conn.QueryContext(ctx, listTenantSummariesPagedSQL, search, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("listing tenant summaries paged: %w", err)
	}
	defer rows.Close()

	var tenants []TenantSummary
	for rows.Next() {
		var t TenantSummary
		var lastSignIn sql.NullTime
		if err := rows.Scan(
			&t.Username, &t.Email, &t.Name,
			&t.Plan, &t.Status, &t.MaxRepos, &t.MaxEventsPerWeek,
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
	return &TenantSummaryPage{Tenants: tenants, Total: total}, nil
}

// ListTenantSummaries returns all tenants with their last sign-in time.
func ListTenantSummaries(ctx context.Context, db *sql.DB) ([]TenantSummary, error) {
	conn, err := adminConn(ctx, db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	rows, err := conn.QueryContext(ctx, listTenantSummariesSQL)
	if err != nil {
		return nil, fmt.Errorf("listing tenant summaries: %w", err)
	}
	defer rows.Close()

	var tenants []TenantSummary
	for rows.Next() {
		var t TenantSummary
		var lastSignIn sql.NullTime
		if err := rows.Scan(
			&t.Username, &t.Email, &t.Name,
			&t.Plan, &t.Status, &t.MaxRepos, &t.MaxEventsPerWeek,
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
	conn, err := adminConn(ctx, db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	var td TenantDetail
	var lastSignIn, digestLastSent sql.NullTime
	var email sql.NullString
	err = conn.QueryRowContext(ctx, getTenantDetailByUsernameSQL, username).Scan(
		&td.ID, &td.Username, &email,
		&td.Name, &td.Company, &td.Location, &td.Bio,
		&td.Plan, &td.Status, &td.MaxRepos, &td.MaxEventsPerWeek,
		&td.CreatedAt, &lastSignIn, &digestLastSent,
	)
	if err != nil {
		return nil, fmt.Errorf("getting tenant detail for %q: %w", username, err)
	}
	if lastSignIn.Valid {
		td.LastSignIn = &lastSignIn.Time
	}
	if digestLastSent.Valid {
		td.DigestLastSentAt = &digestLastSent.Time
	}
	if email.Valid {
		td.Email = email.String
	}
	return &td, nil
}

// GetTenantRepoDetails returns per-repo statistics for a tenant.
// backfillSince scopes PR backfill counts to match the worker's BACKFILL_MAX_DAYS window.
func GetTenantRepoDetails(ctx context.Context, db *sql.DB, tenantID, since, weekStart, backfillSince string) ([]RepoDetail, error) {
	conn, err := adminConn(ctx, db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	rows, err := conn.QueryContext(ctx, getTenantRepoDetailsSQL, tenantID, since, weekStart, backfillSince)
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
			&rd.Contributors, &rd.Scored,
		); err != nil {
			return nil, fmt.Errorf("scanning repo detail: %w", err)
		}
		rd.BackfillTarget = data.EventAgeDaysDefault
		repos = append(repos, rd)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating repo details: %w", err)
	}

	// Populate backfill coverage from state table — reuse the same scoped
	// connection so all queries see the cleared tenant scope.
	for i := range repos {
		var backfillUnix sql.NullInt64
		err := conn.QueryRowContext(ctx, selectMinBackfillSQL,
			repos[i].Org, repos[i].Repo).Scan(&backfillUnix)
		if err != nil || !backfillUnix.Valid {
			continue
		}
		backfillTime := time.Unix(backfillUnix.Int64, 0).UTC()
		repos[i].BackfillDays = int(time.Since(backfillTime).Hours() / 24)
		if repos[i].BackfillDays > repos[i].BackfillTarget {
			repos[i].BackfillDays = repos[i].BackfillTarget
		}
	}

	return repos, nil
}

// ImportErrorRepo holds a repo with import errors, including which tenant tracks it.
type ImportErrorRepo struct {
	Org       string
	Repo      string
	Errors    int
	LastError string
	Username  string
}

const listImportErrorReposSQL = `
	SELECT tr.org, tr.repo, tr.import_errors, COALESCE(tr.import_last_error, ''), t.username
	FROM devpulse_tenant_repo tr
	JOIN devpulse_tenant t ON t.id = tr.tenant_id
	WHERE tr.active = TRUE AND tr.import_errors > 0
	ORDER BY tr.import_errors DESC, tr.org, tr.repo`

// ListImportErrorRepos returns all active repos with import errors, including tenant context.
func ListImportErrorRepos(ctx context.Context, db *sql.DB) ([]ImportErrorRepo, error) {
	conn, err := adminConn(ctx, db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	rows, err := conn.QueryContext(ctx, listImportErrorReposSQL)
	if err != nil {
		return nil, fmt.Errorf("listing import error repos: %w", err)
	}
	defer rows.Close()

	var out []ImportErrorRepo
	for rows.Next() {
		var r ImportErrorRepo
		if err := rows.Scan(&r.Org, &r.Repo, &r.Errors, &r.LastError, &r.Username); err != nil {
			return nil, fmt.Errorf("scanning import error repo: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating import error repos: %w", err)
	}
	return out, nil
}

// TenantWithoutInstall is a tenant consuming imports but contributing no token to the pool.
type TenantWithoutInstall struct {
	Username    string
	Plan        string
	ActiveRepos int
}

const listTenantsWithoutInstallSQL = `
	SELECT t.username, t.plan, COUNT(tr.id) AS active_repos
	FROM devpulse_tenant t
	JOIN devpulse_tenant_repo tr ON tr.tenant_id = t.id AND tr.active = TRUE
	WHERE t.status = 'active'
	  AND t.tos_accepted_at IS NOT NULL
	  AND NOT EXISTS (
	    SELECT 1 FROM devpulse_github_app_installation gi
	    WHERE gi.tenant_id = t.id
	      AND gi.suspended_at IS NULL
	  )
	GROUP BY t.id, t.username, t.plan
	ORDER BY active_repos DESC, t.username`

// ListTenantsWithoutInstall returns active tenants that have repos but no GitHub App installation.
func ListTenantsWithoutInstall(ctx context.Context, db *sql.DB) ([]TenantWithoutInstall, error) {
	conn, err := adminConn(ctx, db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	rows, err := conn.QueryContext(ctx, listTenantsWithoutInstallSQL)
	if err != nil {
		return nil, fmt.Errorf("listing tenants without install: %w", err)
	}
	defer rows.Close()

	var out []TenantWithoutInstall
	for rows.Next() {
		var t TenantWithoutInstall
		if err := rows.Scan(&t.Username, &t.Plan, &t.ActiveRepos); err != nil {
			return nil, fmt.Errorf("scanning tenant without install: %w", err)
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating tenants without install: %w", err)
	}
	return out, nil
}
