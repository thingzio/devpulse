package tenant

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
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
	Sample       bool    `json:"sample"`
	LastImportAt *string `json:"last_import_at"`
}

// OrgRepo is a simple org/repo pair.
type OrgRepo struct {
	Org  string
	Repo string
}

const saveInstallationSQL = `
	INSERT INTO devpulse_github_app_installation (tenant_id, installation_id, target_type, target_login, permissions, app_id)
	VALUES ($1, $2, $3, $4, $5, $6)
	ON CONFLICT (installation_id) DO UPDATE SET
		tenant_id = EXCLUDED.tenant_id,
		target_type = EXCLUDED.target_type,
		target_login = EXCLUDED.target_login,
		permissions = EXCLUDED.permissions,
		app_id = EXCLUDED.app_id,
		suspended_at = NULL`

const listInstallationsSQL = `
	SELECT id, tenant_id, installation_id, target_type, target_login, suspended_at, created_at
	FROM devpulse_github_app_installation WHERE tenant_id = $1 ORDER BY created_at`

// Pinned to NULL so a retried suspend webhook (GitHub re-delivers on
// transient 5xx) preserves the original suspension timestamp instead of
// rewriting it on each retry.
const suspendInstallationSQL = `UPDATE devpulse_github_app_installation
	SET suspended_at = NOW()
	WHERE installation_id = $1 AND suspended_at IS NULL`

const addTenantRepoSQL = `
	INSERT INTO devpulse_tenant_repo (tenant_id, org, repo)
	VALUES ($1, $2, $3)
	ON CONFLICT (tenant_id, org, repo) DO UPDATE SET active = TRUE`

const listTenantReposSQL = `
	SELECT tr.id, tr.tenant_id, tr.org, tr.repo, tr.active, tr.sample, rm.last_import_at
	FROM devpulse_tenant_repo tr
	LEFT JOIN devpulse_repo_meta rm ON rm.org = tr.org AND rm.repo = tr.repo
	WHERE tr.tenant_id = $1 AND tr.active = TRUE ORDER BY tr.org, tr.repo`

const deactivateTenantRepoSQL = `
	UPDATE devpulse_tenant_repo SET active = FALSE WHERE tenant_id = $1 AND org = $2 AND repo = $3`

const countTenantReposSQL = `SELECT COUNT(*) FROM devpulse_tenant_repo WHERE tenant_id = $1 AND active = TRUE AND sample = FALSE`

const getTenantMaxReposSQL = `SELECT max_repos FROM devpulse_tenant WHERE id = $1`

const getInstallationForOrgSQL = `
	SELECT installation_id, target_login
	FROM devpulse_github_app_installation
	WHERE tenant_id = $1 AND target_login = $2 AND suspended_at IS NULL
	  AND ($3 = 0 OR app_id IS NULL OR app_id = $3)
	LIMIT 1`

// SaveInstallation stores or updates a GitHub App installation for a tenant.
// appID is the GitHub App ID from the webhook payload (installation.app_id).
func SaveInstallation(ctx context.Context, db *sql.DB, tenantID string, installationID int64, targetType, targetLogin string, permissions []byte, appID int64) error {
	_, err := db.ExecContext(ctx, saveInstallationSQL, tenantID, installationID, targetType, targetLogin, permissions, appID)
	if err != nil {
		return fmt.Errorf("saving installation: %w", err)
	}
	return nil
}

//nolint:dupl // same row-scan pattern as ListTenantRepos but different query/type
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
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating installations: %w", err)
	}
	return result, nil
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
	defer func() { _ = tx.Rollback() }()

	for _, r := range repos {
		if _, err := tx.ExecContext(ctx, addTenantRepoSQL, tenantID, r.Org, r.Repo); err != nil {
			return fmt.Errorf("adding repo %s/%s: %w", r.Org, r.Repo, err)
		}
	}

	var maxRepos int
	if err := tx.QueryRowContext(ctx, getTenantMaxReposSQL, tenantID).Scan(&maxRepos); err != nil {
		return fmt.Errorf("getting max repos: %w", err)
	}

	var currentCount int
	if err := tx.QueryRowContext(ctx, countTenantReposSQL, tenantID).Scan(&currentCount); err != nil {
		return fmt.Errorf("counting repos: %w", err)
	}

	if maxRepos > 0 && currentCount > maxRepos {
		return fmt.Errorf("adding repos would exceed plan limit (%d): %w", maxRepos, ErrRepoLimitExceeded)
	}

	return tx.Commit()
}

// CountTenantRepos returns the number of active repos for a tenant.
func CountTenantRepos(ctx context.Context, db *sql.DB, tenantID string) (int, error) {
	var count int
	if err := db.QueryRowContext(ctx, countTenantReposSQL, tenantID).Scan(&count); err != nil {
		return 0, fmt.Errorf("counting tenant repos: %w", err)
	}
	return count, nil
}

//nolint:dupl // same row-scan pattern as ListInstallations but different query/type
func ListTenantRepos(ctx context.Context, db *sql.DB, tenantID string) ([]TenantRepo, error) {
	rows, err := db.QueryContext(ctx, listTenantReposSQL, tenantID)
	if err != nil {
		return nil, fmt.Errorf("listing repos: %w", err)
	}
	defer rows.Close()

	var result []TenantRepo
	for rows.Next() {
		var r TenantRepo
		if err := rows.Scan(&r.ID, &r.TenantID, &r.Org, &r.Repo, &r.Active, &r.Sample, &r.LastImportAt); err != nil {
			return nil, fmt.Errorf("scanning repo: %w", err)
		}
		result = append(result, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating tenant repos: %w", err)
	}
	return result, nil
}

// DeactivateTenantRepo marks a repo as inactive.
func DeactivateTenantRepo(ctx context.Context, db *sql.DB, tenantID, org, repo string) error {
	_, err := db.ExecContext(ctx, deactivateTenantRepoSQL, tenantID, org, repo)
	if err != nil {
		return fmt.Errorf("deactivating repo: %w", err)
	}
	return nil
}

const addSampleRepoSQL = `
	INSERT INTO devpulse_tenant_repo (tenant_id, org, repo, sample)
	VALUES ($1, $2, $3, TRUE)
	ON CONFLICT (tenant_id, org, repo) DO UPDATE SET active = TRUE, sample = TRUE`

const markRepoSampleSQL = `
	UPDATE devpulse_tenant_repo SET sample = TRUE
	WHERE tenant_id = $1 AND org = $2 AND repo = $3 AND active = TRUE`

const sampleRepoHasDataSQL = `SELECT EXISTS(SELECT 1 FROM devpulse_repo_meta WHERE org = $1 AND repo = $2)`

// AddSampleRepos inserts sample repos for a tenant, bypassing plan limits.
// Logs a warning for any repo that has no imported data yet.
func AddSampleRepos(ctx context.Context, db *sql.DB, tenantID string, repos []OrgRepo) error {
	for _, r := range repos {
		if _, err := db.ExecContext(ctx, addSampleRepoSQL, tenantID, r.Org, r.Repo); err != nil {
			return fmt.Errorf("adding sample repo %s/%s: %w", r.Org, r.Repo, err)
		}

		var exists bool
		if err := db.QueryRowContext(ctx, sampleRepoHasDataSQL, r.Org, r.Repo).Scan(&exists); err != nil {
			slog.Warn("checking sample repo data", "org", r.Org, "repo", r.Repo, "error", err)
		} else if !exists {
			slog.Warn("sample repo has no imported data", "org", r.Org, "repo", r.Repo)
		}
	}
	return nil
}

// MarkRepoAsSample marks an existing tenant repo as a sample.
func MarkRepoAsSample(ctx context.Context, db *sql.DB, tenantID, org, repo string) error {
	if _, err := db.ExecContext(ctx, markRepoSampleSQL, tenantID, org, repo); err != nil {
		return fmt.Errorf("marking repo %s/%s as sample: %w", org, repo, err)
	}
	return nil
}

// GetInstallationForOrg returns the active installation for a tenant's org, if any.
// appID filters to installations belonging to this GitHub App (0 means no filter).
func GetInstallationForOrg(ctx context.Context, db *sql.DB, tenantID, org string, appID int64) (*ActiveInstallation, error) {
	var inst ActiveInstallation
	err := db.QueryRowContext(ctx, getInstallationForOrgSQL, tenantID, org, appID).Scan(&inst.ID, &inst.Login)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("querying installation for org %s: %w", org, err)
	}
	return &inst, nil
}
