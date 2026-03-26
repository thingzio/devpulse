package tenant

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

var errRepoLimitExceeded = errors.New("repo limit exceeded for plan")

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
	ID       string
	TenantID string
	Org      string
	Repo     string
	Active   bool
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
	SELECT id, tenant_id, org, repo, active
	FROM tenant_repo WHERE tenant_id = $1 AND active = TRUE ORDER BY org, repo`

const deactivateTenantRepoSQL = `
	UPDATE tenant_repo SET active = FALSE WHERE tenant_id = $1 AND org = $2 AND repo = $3`

const countTenantReposSQL = `SELECT COUNT(*) FROM tenant_repo WHERE tenant_id = $1 AND active = TRUE`

const getTenantMaxReposSQL = `SELECT max_repos FROM tenant WHERE id = $1`

// SaveInstallation stores or updates a GitHub App installation for a tenant.
func SaveInstallation(db *sql.DB, tenantID string, installationID int64, targetType, targetLogin string, permissions []byte) error {
	_, err := db.Exec(saveInstallationSQL, tenantID, installationID, targetType, targetLogin, permissions)
	if err != nil {
		return fmt.Errorf("saving installation: %w", err)
	}
	return nil
}

// ListInstallations returns all installations for a tenant.
func ListInstallations(db *sql.DB, tenantID string) ([]Installation, error) {
	rows, err := db.Query(listInstallationsSQL, tenantID)
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
func SuspendInstallation(db *sql.DB, installationID int64) error {
	_, err := db.Exec(suspendInstallationSQL, installationID)
	if err != nil {
		return fmt.Errorf("suspending installation: %w", err)
	}
	return nil
}

// AddTenantRepos adds repos to a tenant's tracking list, enforcing the plan limit.
func AddTenantRepos(db *sql.DB, tenantID string, repos []OrgRepo) error {
	var maxRepos int
	if err := db.QueryRow(getTenantMaxReposSQL, tenantID).Scan(&maxRepos); err != nil {
		return fmt.Errorf("getting max repos: %w", err)
	}

	var currentCount int
	if err := db.QueryRow(countTenantReposSQL, tenantID).Scan(&currentCount); err != nil {
		return fmt.Errorf("counting repos: %w", err)
	}

	if currentCount+len(repos) > maxRepos {
		return fmt.Errorf("%w: %d + %d > %d", errRepoLimitExceeded, currentCount, len(repos), maxRepos)
	}

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("beginning tx: %w", err)
	}

	for _, r := range repos {
		if _, err := tx.Exec(addTenantRepoSQL, tenantID, r.Org, r.Repo); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("adding repo %s/%s: %w", r.Org, r.Repo, err)
		}
	}

	return tx.Commit()
}

// ListTenantRepos returns all active repos for a tenant.
func ListTenantRepos(db *sql.DB, tenantID string) ([]TenantRepo, error) {
	rows, err := db.Query(listTenantReposSQL, tenantID)
	if err != nil {
		return nil, fmt.Errorf("listing repos: %w", err)
	}
	defer rows.Close()

	var result []TenantRepo
	for rows.Next() {
		var r TenantRepo
		if err := rows.Scan(&r.ID, &r.TenantID, &r.Org, &r.Repo, &r.Active); err != nil {
			return nil, fmt.Errorf("scanning repo: %w", err)
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

// DeactivateTenantRepo marks a repo as inactive.
func DeactivateTenantRepo(db *sql.DB, tenantID, org, repo string) error {
	_, err := db.Exec(deactivateTenantRepoSQL, tenantID, org, repo)
	if err != nil {
		return fmt.Errorf("deactivating repo: %w", err)
	}
	return nil
}
