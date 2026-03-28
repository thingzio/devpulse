package tenant

import (
	"context"
	"database/sql"
	"fmt"
)

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
	return tenants, rows.Err()
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
	return result, rows.Err()
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
	return repos, rows.Err()
}
