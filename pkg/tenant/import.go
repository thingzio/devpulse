package tenant

import (
	"context"
	"database/sql"
	"fmt"
)

const incrementImportErrorsSQL = `
	UPDATE tenant_repo
	SET import_errors = import_errors + 1, import_last_error = $2
	WHERE id = $1`

const resetImportErrorsSQL = `
	UPDATE tenant_repo
	SET import_errors = 0, import_last_error = NULL
	WHERE id = $1`

const resetImportErrorsByRepoSQL = `
	UPDATE tenant_repo
	SET import_errors = 0, import_last_error = NULL
	WHERE org = $1 AND repo = $2 AND import_errors > 0`

// IncrementImportErrors increments the consecutive error counter and stores the last error.
func IncrementImportErrors(ctx context.Context, db *sql.DB, id, errMsg string) error {
	if _, err := db.ExecContext(ctx, incrementImportErrorsSQL, id, errMsg); err != nil {
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
	FROM tenant t
	JOIN tenant_repo tr ON tr.tenant_id = t.id
	WHERE tr.active = TRUE AND t.tos_accepted_at IS NOT NULL`

const getActiveInstallationsSQL = `
	SELECT installation_id, target_login
	FROM github_app_installation
	WHERE tenant_id = $1 AND suspended_at IS NULL`

const getActiveReposForInstallSQL = `
	SELECT tr.org, tr.repo
	FROM tenant_repo tr
	JOIN github_app_installation gi ON gi.tenant_id = tr.tenant_id AND gi.target_login = tr.org
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
func GetActiveInstallations(ctx context.Context, db *sql.DB, tenantID string) ([]ActiveInstallation, error) {
	rows, err := db.QueryContext(ctx, getActiveInstallationsSQL, tenantID)
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
	       COALESCE(ec.cnt, 0) AS event_count
	FROM tenant_repo tr
	JOIN tenant t ON t.id = tr.tenant_id
	LEFT JOIN (
		SELECT org, repo, COUNT(*) AS cnt
		FROM event
		GROUP BY org, repo
	) ec ON ec.org = tr.org AND ec.repo = tr.repo
	WHERE tr.active = TRUE
	  AND tr.import_errors < 5
	  AND t.tos_accepted_at IS NOT NULL
	  AND (tr.import_done_at IS NOT NULL OR tr.created_at < NOW() - MAKE_INTERVAL(mins => $1))
	ORDER BY lower(tr.repo), lower(tr.org), tr.tenant_id`

const listImportWorkForRepoSQL = `
	SELECT tr.id, tr.tenant_id, tr.org, tr.repo, t.plan,
	       COALESCE(ec.cnt, 0) AS event_count
	FROM tenant_repo tr
	JOIN tenant t ON t.id = tr.tenant_id
	LEFT JOIN (
		SELECT org, repo, COUNT(*) AS cnt
		FROM event
		GROUP BY org, repo
	) ec ON ec.org = tr.org AND ec.repo = tr.repo
	WHERE tr.active = TRUE
	  AND tr.org = $1
	  AND tr.repo = $2
	  AND t.tos_accepted_at IS NOT NULL
	ORDER BY tr.tenant_id`

// ListImportWorkForRepo returns import work rows for a single repo across all tenants.
func ListImportWorkForRepo(ctx context.Context, db *sql.DB, org, repo string) ([]ImportWorkRow, error) {
	rows, err := db.QueryContext(ctx, listImportWorkForRepoSQL, org, repo)
	if err != nil {
		return nil, fmt.Errorf("listing import work for %s/%s: %w", org, repo, err)
	}
	return scanImportWorkRows(rows)
}

const markImportDoneByRepoSQL = `
	UPDATE tenant_repo
	SET import_done_at = NOW()
	WHERE org = $1 AND repo = $2 AND active = TRUE`

const incrementImportErrorsByRepoSQL = `
	UPDATE tenant_repo
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
func ListImportWork(ctx context.Context, db *sql.DB, adoptTimeoutMin int) ([]ImportWorkRow, error) {
	rows, err := db.QueryContext(ctx, listImportWorkSQL, adoptTimeoutMin)
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

// MarkImportDoneByRepo marks all active tenant_repo rows for a given org/repo as imported.
func MarkImportDoneByRepo(ctx context.Context, db *sql.DB, org, repo string) error {
	if _, err := db.ExecContext(ctx, markImportDoneByRepoSQL, org, repo); err != nil {
		return fmt.Errorf("marking import done for %s/%s: %w", org, repo, err)
	}
	return nil
}

// IncrementImportErrorsByRepo increments error counter for all active tenant_repo rows of a given org/repo.
func IncrementImportErrorsByRepo(ctx context.Context, db *sql.DB, org, repo, errMsg string) error {
	if _, err := db.ExecContext(ctx, incrementImportErrorsByRepoSQL, org, repo, errMsg); err != nil {
		return fmt.Errorf("incrementing import errors for %s/%s: %w", org, repo, err)
	}
	return nil
}

// GetActiveReposForInstall returns active repos for a tenant's installation.
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
