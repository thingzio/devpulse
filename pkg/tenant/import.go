package tenant

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
)

// importErrorMaxLen bounds the size of error strings stored in the
// import_last_error column. Prevents pathological GitHub/Anthropic error
// bodies (HTML pages, full prompt echoes) from flooding the row.
const importErrorMaxLen = 1024

// secret-bearing tokens we may receive in error strings:
//   - GitHub Personal Access Tokens: ghp_, gho_, ghu_, ghs_, ghr_
//   - GitHub installation tokens: ghs_
//   - Bearer prefixes from upstream services
//   - Generic "Authorization: <scheme> <token>" headers
var (
	importErrTokenRE  = regexp.MustCompile(`gh[opusr]_[A-Za-z0-9]{20,}`)
	importErrBearerRE = regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._\-]+`)
	importErrAuthRE   = regexp.MustCompile(`(?i)authorization:\s*\S+\s+\S+`)
)

// sanitizeImportError redacts secrets and truncates long error messages
// before they are persisted in the database.
func sanitizeImportError(s string) string {
	s = importErrTokenRE.ReplaceAllString(s, "[REDACTED]")
	s = importErrBearerRE.ReplaceAllString(s, "Bearer [REDACTED]")
	s = importErrAuthRE.ReplaceAllString(s, "Authorization: [REDACTED]")
	if len(s) > importErrorMaxLen {
		s = s[:importErrorMaxLen] + "...[truncated]"
	}
	return s
}

const incrementImportErrorsSQL = `
	UPDATE devpulse_tenant_repo
	SET import_errors = import_errors + 1, import_last_error = $2
	WHERE id = $1`

const resetImportErrorsSQL = `
	UPDATE devpulse_tenant_repo
	SET import_errors = 0, import_last_error = NULL
	WHERE id = $1`

const resetImportErrorsByRepoSQL = `
	UPDATE devpulse_tenant_repo
	SET import_errors = 0, import_last_error = NULL
	WHERE org = $1 AND repo = $2 AND import_errors > 0`

const deleteStateByRepoSQL = `DELETE FROM devpulse_state WHERE org = $1 AND repo = $2`

const deleteRepoMetaByRepoSQL = `DELETE FROM devpulse_repo_meta WHERE org = $1 AND repo = $2`

// IncrementImportErrors increments the consecutive error counter and stores the last error.
func IncrementImportErrors(ctx context.Context, db *sql.DB, id, errMsg string) error {
	if _, err := db.ExecContext(ctx, incrementImportErrorsSQL, id, sanitizeImportError(errMsg)); err != nil {
		return fmt.Errorf("incrementing import errors: %w", err)
	}
	return nil
}

// ResetImportErrors clears the error counter and last error after a successful import.
func ResetImportErrors(ctx context.Context, db *sql.DB, id string) error {
	if _, err := db.ExecContext(ctx, resetImportErrorsSQL, id); err != nil {
		return fmt.Errorf("resetting import errors: %w", err)
	}
	return nil
}

// ResetImportErrorsByRepo clears the error counter for a specific org/repo across all tenants.
func ResetImportErrorsByRepo(ctx context.Context, db *sql.DB, org, repo string) (int64, error) {
	res, err := db.ExecContext(ctx, resetImportErrorsByRepoSQL, org, repo)
	if err != nil {
		return 0, fmt.Errorf("resetting import errors for %s/%s: %w", org, repo, err)
	}
	return res.RowsAffected()
}

// HardResetRepo clears error counter, import state, and repo metadata for a
// specific org/repo. Forces a full reimport on the next scheduled run.
func HardResetRepo(ctx context.Context, db *sql.DB, org, repo string) (int64, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("starting transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.ExecContext(ctx, resetImportErrorsByRepoSQL, org, repo)
	if err != nil {
		return 0, fmt.Errorf("resetting import errors for %s/%s: %w", org, repo, err)
	}

	if _, err := tx.ExecContext(ctx, deleteStateByRepoSQL, org, repo); err != nil {
		return 0, fmt.Errorf("clearing state for %s/%s: %w", org, repo, err)
	}

	if _, err := tx.ExecContext(ctx, deleteRepoMetaByRepoSQL, org, repo); err != nil {
		return 0, fmt.Errorf("clearing repo meta for %s/%s: %w", org, repo, err)
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("committing hard reset for %s/%s: %w", org, repo, err)
	}

	return res.RowsAffected()
}

// ActiveTenant represents a tenant with at least one active repo.
type ActiveTenant struct {
	ID       string
	Username string
}

// ActiveRepo is a repo eligible for import.
type ActiveRepo struct {
	Org  string
	Repo string
}

const getActiveTenantsSQL = `
	SELECT DISTINCT t.id, t.username
	FROM devpulse_tenant t
	JOIN devpulse_tenant_repo tr ON tr.tenant_id = t.id
	WHERE tr.active = TRUE AND t.tos_accepted_at IS NOT NULL
	  AND t.status = 'active'`

const getActiveInstallationsSQL = `
	SELECT installation_id, target_login
	FROM devpulse_github_app_installation
	WHERE tenant_id = $1 AND suspended_at IS NULL
	  AND ($2 = 0 OR app_id IS NULL OR app_id = $2)`

const getActiveReposForInstallSQL = `
	SELECT tr.org, tr.repo
	FROM devpulse_tenant_repo tr
	JOIN devpulse_github_app_installation gi ON gi.tenant_id = tr.tenant_id AND gi.target_login = tr.org
	WHERE tr.tenant_id = $1 AND gi.installation_id = $2 AND tr.active = TRUE`

// ActiveInstallation holds the GitHub installation ID and target org login.
type ActiveInstallation struct {
	ID    int64
	Login string
}

// GetActiveTenants returns all tenants that have accepted ToS and have active repos.
func GetActiveTenants(ctx context.Context, db *sql.DB) ([]ActiveTenant, error) {
	rows, err := db.QueryContext(ctx, getActiveTenantsSQL)
	if err != nil {
		return nil, fmt.Errorf("querying active tenants: %w", err)
	}
	defer rows.Close()

	var tenants []ActiveTenant
	for rows.Next() {
		var t ActiveTenant
		if err := rows.Scan(&t.ID, &t.Username); err != nil {
			return nil, fmt.Errorf("scanning tenant: %w", err)
		}
		tenants = append(tenants, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating active tenants: %w", err)
	}
	return tenants, nil
}

// GetActiveInstallations returns non-suspended installations for a tenant.
// appID filters to installations belonging to this GitHub App (0 means no filter).
//
//nolint:dupl // same row-scan pattern as GetActiveReposForInstall but different query/type
func GetActiveInstallations(ctx context.Context, db *sql.DB, tenantID string, appID int64) ([]ActiveInstallation, error) {
	rows, err := db.QueryContext(ctx, getActiveInstallationsSQL, tenantID, appID)
	if err != nil {
		return nil, fmt.Errorf("querying installations: %w", err)
	}
	defer rows.Close()

	var result []ActiveInstallation
	for rows.Next() {
		var inst ActiveInstallation
		if err := rows.Scan(&inst.ID, &inst.Login); err != nil {
			return nil, fmt.Errorf("scanning installation: %w", err)
		}
		result = append(result, inst)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating active installations: %w", err)
	}
	return result, nil
}

const listImportWorkSQL = `
	SELECT tr.id, tr.tenant_id, tr.org, tr.repo, t.plan,
	       (SELECT COUNT(*) FROM devpulse_event e
	        WHERE e.org = tr.org AND e.repo = tr.repo) AS event_count
	FROM devpulse_tenant_repo tr
	JOIN devpulse_tenant t ON t.id = tr.tenant_id
	WHERE tr.active = TRUE
	  AND tr.import_errors < 5
	  AND t.tos_accepted_at IS NOT NULL
	  AND t.status = 'active'
	ORDER BY lower(tr.repo), lower(tr.org), tr.tenant_id`

const listImportWorkForRepoSQL = `
	SELECT tr.id, tr.tenant_id, tr.org, tr.repo, t.plan,
	       (SELECT COUNT(*) FROM devpulse_event e
	        WHERE e.org = tr.org AND e.repo = tr.repo) AS event_count
	FROM devpulse_tenant_repo tr
	JOIN devpulse_tenant t ON t.id = tr.tenant_id
	WHERE tr.active = TRUE
	  AND tr.org = $1
	  AND tr.repo = $2
	  AND t.tos_accepted_at IS NOT NULL
	  AND t.status = 'active'
	ORDER BY tr.tenant_id`

// ListImportWorkForRepo returns import work rows for a single repo across all tenants.
func ListImportWorkForRepo(ctx context.Context, db *sql.DB, org, repo string) ([]ImportWorkRow, error) {
	rows, err := db.QueryContext(ctx, listImportWorkForRepoSQL, org, repo)
	if err != nil {
		return nil, fmt.Errorf("listing import work for %s/%s: %w", org, repo, err)
	}
	return scanImportWorkRows(rows)
}

const incrementImportErrorsByRepoSQL = `
	UPDATE devpulse_tenant_repo
	SET import_errors = import_errors + 1, import_last_error = $3
	WHERE org = $1 AND repo = $2 AND active = TRUE`

// ImportWorkRow is a raw row from the import work list query.
type ImportWorkRow struct {
	TenantRepoID string
	TenantID     string
	Org          string
	Repo         string
	Plan         string
	EventCount   int
}

// ListImportWork returns all active tenant_repo rows eligible for import,
// joined with tenant plan. Sorted deterministically for sharding.
func ListImportWork(ctx context.Context, db *sql.DB) ([]ImportWorkRow, error) {
	rows, err := db.QueryContext(ctx, listImportWorkSQL)
	if err != nil {
		return nil, fmt.Errorf("listing import work: %w", err)
	}
	return scanImportWorkRows(rows)
}

// scanImportWorkRows scans sql.Rows into ImportWorkRow slices.
func scanImportWorkRows(rows *sql.Rows) ([]ImportWorkRow, error) {
	defer rows.Close()

	var result []ImportWorkRow
	for rows.Next() {
		var r ImportWorkRow
		if err := rows.Scan(&r.TenantRepoID, &r.TenantID, &r.Org, &r.Repo, &r.Plan, &r.EventCount); err != nil {
			return nil, fmt.Errorf("scanning import work row: %w", err)
		}
		result = append(result, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating import work rows: %w", err)
	}
	return result, nil
}

// IncrementImportErrorsByRepo increments error counter for all active tenant_repo rows of a given org/repo.
func IncrementImportErrorsByRepo(ctx context.Context, db *sql.DB, org, repo, errMsg string) error {
	if _, err := db.ExecContext(ctx, incrementImportErrorsByRepoSQL, org, repo, sanitizeImportError(errMsg)); err != nil {
		return fmt.Errorf("incrementing import errors for %s/%s: %w", org, repo, err)
	}
	return nil
}

// GetActiveReposForInstall returns active repos for a tenant's installation.
//
//nolint:dupl // same row-scan pattern as GetActiveInstallations but different query/type
func GetActiveReposForInstall(ctx context.Context, db *sql.DB, tenantID string, installationID int64) ([]ActiveRepo, error) {
	rows, err := db.QueryContext(ctx, getActiveReposForInstallSQL, tenantID, installationID)
	if err != nil {
		return nil, fmt.Errorf("querying repos: %w", err)
	}
	defer rows.Close()

	var repos []ActiveRepo
	for rows.Next() {
		var r ActiveRepo
		if err := rows.Scan(&r.Org, &r.Repo); err != nil {
			return nil, fmt.Errorf("scanning repo: %w", err)
		}
		repos = append(repos, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating active repos: %w", err)
	}
	return repos, nil
}
