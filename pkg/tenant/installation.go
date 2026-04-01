package tenant

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrRepoLimitExceeded is returned when adding repos would exceed the tenant's plan limit.
var ErrRepoLimitExceeded = errors.New("repo limit exceeded for plan")

// Installation represents a GitHub App installation for a tenant.
type Installation struct {
	ID             string
	TenantID       string
	InstallationID int64
	TargetType     string
	TargetLogin    string
	SuspendedAt    *time.Time
	CreatedAt      time.Time
}

// TenantRepo represents a repo tracked by a tenant.
type TenantRepo struct {
	ID           string  `json:"id"`
	TenantID     string  `json:"tenant_id"`
	Org          string  `json:"org"`
	Repo         string  `json:"repo"`
	Active       bool    `json:"active"`
	LastImportAt *string `json:"last_import_at"`
}

// OrgRepo is a simple org/repo pair.
type OrgRepo struct {
	Org  string
	Repo string
}

const saveInstallationSQL = `
	INSERT INTO github_app_installation (tenant_id, installation_id, target_type, target_login, permissions)
	VALUES ($1, $2, $3, $4, $5)
	ON CONFLICT (installation_id) DO UPDATE SET
		tenant_id = EXCLUDED.tenant_id,
		target_type = EXCLUDED.target_type,
		target_login = EXCLUDED.target_login,
		permissions = EXCLUDED.permissions,
		suspended_at = NULL`

const listInstallationsSQL = `
	SELECT id, tenant_id, installation_id, target_type, target_login, suspended_at, created_at
	FROM github_app_installation WHERE tenant_id = $1 ORDER BY created_at`

const suspendInstallationSQL = `UPDATE github_app_installation SET suspended_at = NOW() WHERE installation_id = $1`

const addTenantRepoSQL = `
	INSERT INTO tenant_repo (tenant_id, org, repo)
	VALUES ($1, $2, $3)
	ON CONFLICT (tenant_id, org, repo) DO UPDATE SET active = TRUE`

const listTenantReposSQL = `
	SELECT tr.id, tr.tenant_id, tr.org, tr.repo, tr.active, rm.last_import_at
	FROM tenant_repo tr
	LEFT JOIN repo_meta rm ON rm.org = tr.org AND rm.repo = tr.repo
	WHERE tr.tenant_id = $1 AND tr.active = TRUE ORDER BY tr.org, tr.repo`

const deactivateTenantRepoSQL = `
	UPDATE tenant_repo SET active = FALSE WHERE tenant_id = $1 AND org = $2 AND repo = $3`

const countTenantReposSQL = `SELECT COUNT(*) FROM tenant_repo WHERE tenant_id = $1 AND active = TRUE`

const getTenantMaxReposSQL = `SELECT max_repos FROM tenant WHERE id = $1`

const getInstallationForOrgSQL = `
	SELECT installation_id, target_login
	FROM github_app_installation
	WHERE tenant_id = $1 AND target_login = $2 AND suspended_at IS NULL
	LIMIT 1`

// SaveInstallation stores or updates a GitHub App installation for a tenant.
func SaveInstallation(ctx context.Context, db *sql.DB, tenantID string, installationID int64, targetType, targetLogin string, permissions []byte) error {
	_, err := db.ExecContext(ctx, saveInstallationSQL, tenantID, installationID, targetType, targetLogin, permissions)
	if err != nil {
		return fmt.Errorf("saving installation: %w", err)
	}
	return nil
}

// ListInstallations returns all installations for a tenant.
func ListInstallations(ctx context.Context, db *sql.DB, tenantID string) ([]Installation, error) {
	rows, err := db.QueryContext(ctx, listInstallationsSQL, tenantID)
	if err != nil {
		return nil, fmt.Errorf("listing installations: %w", err)
	}
	defer rows.Close()

	var result []Installation
	for rows.Next() {
		var i Installation
		if err := rows.Scan(&i.ID, &i.TenantID, &i.InstallationID, &i.TargetType, &i.TargetLogin, &i.SuspendedAt, &i.CreatedAt); err != nil {
			return nil, fmt.Errorf("scanning installation: %w", err)
		}
		result = append(result, i)
	}
	return result, rows.Err()
}

// SuspendInstallation marks an installation as suspended.
func SuspendInstallation(ctx context.Context, db *sql.DB, installationID int64) error {
	_, err := db.ExecContext(ctx, suspendInstallationSQL, installationID)
	if err != nil {
		return fmt.Errorf("suspending installation: %w", err)
	}
	return nil
}

// AddTenantRepos adds repos to a tenant's tracking list, enforcing the plan limit.
// Upserts run first (ON CONFLICT re-activates deactivated repos without inflating the
// count), then the resulting active count is checked against the plan limit and rolled
// back if exceeded. This prevents the previous bug where re-adding a deactivated repo
// at capacity was incorrectly rejected.
func AddTenantRepos(ctx context.Context, db *sql.DB, tenantID string, repos []OrgRepo) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning tx: %w", err)
	}

	for _, r := range repos {
		if _, err := tx.ExecContext(ctx, addTenantRepoSQL, tenantID, r.Org, r.Repo); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("adding repo %s/%s: %w", r.Org, r.Repo, err)
		}
	}

	var maxRepos int
	if err := tx.QueryRowContext(ctx, getTenantMaxReposSQL, tenantID).Scan(&maxRepos); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("getting max repos: %w", err)
	}

	var currentCount int
	if err := tx.QueryRowContext(ctx, countTenantReposSQL, tenantID).Scan(&currentCount); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("counting repos: %w", err)
	}

	if maxRepos > 0 && currentCount > maxRepos {
		_ = tx.Rollback()
		return fmt.Errorf("adding repos would exceed plan limit (%d): %w", maxRepos, ErrRepoLimitExceeded)
	}

	return tx.Commit()
}

// ListTenantRepos returns all active repos for a tenant.
func ListTenantRepos(ctx context.Context, db *sql.DB, tenantID string) ([]TenantRepo, error) {
	rows, err := db.QueryContext(ctx, listTenantReposSQL, tenantID)
	if err != nil {
		return nil, fmt.Errorf("listing repos: %w", err)
	}
	defer rows.Close()

	var result []TenantRepo
	for rows.Next() {
		var r TenantRepo
		if err := rows.Scan(&r.ID, &r.TenantID, &r.Org, &r.Repo, &r.Active, &r.LastImportAt); err != nil {
			return nil, fmt.Errorf("scanning repo: %w", err)
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

// DeactivateTenantRepo marks a repo as inactive.
func DeactivateTenantRepo(ctx context.Context, db *sql.DB, tenantID, org, repo string) error {
	_, err := db.ExecContext(ctx, deactivateTenantRepoSQL, tenantID, org, repo)
	if err != nil {
		return fmt.Errorf("deactivating repo: %w", err)
	}
	return nil
}

// GetInstallationForOrg returns the active installation for a tenant's org, if any.
func GetInstallationForOrg(ctx context.Context, db *sql.DB, tenantID, org string) (*ActiveInstallation, error) {
	var inst ActiveInstallation
	err := db.QueryRowContext(ctx, getInstallationForOrgSQL, tenantID, org).Scan(&inst.ID, &inst.Login)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("querying installation for org %s: %w", org, err)
	}
	return &inst, nil
}
