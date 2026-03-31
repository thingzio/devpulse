package tenant

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strconv"
)

// ClaimedRepo is a repo claimed from the import queue.
type ClaimedRepo struct {
	ID       string
	TenantID string
	Org      string
	Repo     string
}

// ErrNoWork is returned by ClaimNextRepo when the import queue is empty.
var ErrNoWork = errors.New("no work available")

const resetStaleClaimsSQL = `
	UPDATE tenant_repo
	SET import_claimed_at = NULL, import_claimed_by = NULL
	WHERE import_claimed_at IS NOT NULL
	  AND import_done_at IS NULL
	  AND import_claimed_at < NOW() - INTERVAL '2 hours'`

const resetDoneSQLTmpl = `
	UPDATE tenant_repo
	SET import_claimed_at = NULL, import_claimed_by = NULL, import_done_at = NULL
	WHERE import_done_at IS NOT NULL
	  AND import_done_at < NOW() - INTERVAL '%d minutes'`

const claimNextRepoSQL = `
	UPDATE tenant_repo
	SET import_claimed_at = NOW(), import_claimed_by = $1
	WHERE id = (
		SELECT id FROM tenant_repo
		WHERE active = TRUE AND import_claimed_at IS NULL
		ORDER BY org, repo
		LIMIT 1
		FOR UPDATE SKIP LOCKED
	)
	RETURNING id, tenant_id, org, repo`

const markRepoDoneSQL = `
	UPDATE tenant_repo
	SET import_done_at = NOW()
	WHERE id = $1`

// importResetMinutes returns the IMPORT_RESET_MINUTES env var or 30 as default.
func importResetMinutes() int {
	if v := os.Getenv("IMPORT_RESET_MINUTES"); v != "" {
		if m, err := strconv.Atoi(v); err == nil && m > 0 {
			return m
		}
	}
	return 30
}

// PrepareImportQueue resets stale claims (job died mid-run) and clears completed
// work from the prior cycle so all active repos are available for claiming.
// Only resets repos completed more than IMPORT_RESET_MINUTES ago (default 30).
// Safe to call concurrently — both UPDATEs are idempotent.
func PrepareImportQueue(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning import queue tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // rollback after commit is harmless

	if _, err := tx.ExecContext(ctx, resetStaleClaimsSQL); err != nil {
		return fmt.Errorf("resetting stale claims: %w", err)
	}

	resetDoneSQL := fmt.Sprintf(resetDoneSQLTmpl, importResetMinutes())
	if _, err := tx.ExecContext(ctx, resetDoneSQL); err != nil {
		return fmt.Errorf("resetting completed repos: %w", err)
	}
	return tx.Commit()
}

// ClaimNextRepo atomically claims one unclaimed active repo for import.
// Returns ErrNoWork when the queue is empty.
func ClaimNextRepo(ctx context.Context, db *sql.DB, executionID string) (*ClaimedRepo, error) {
	var r ClaimedRepo
	err := db.QueryRowContext(ctx, claimNextRepoSQL, executionID).
		Scan(&r.ID, &r.TenantID, &r.Org, &r.Repo)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNoWork
	}
	if err != nil {
		return nil, fmt.Errorf("claiming repo: %w", err)
	}
	return &r, nil
}

// MarkRepoDone marks a claimed repo as successfully imported.
func MarkRepoDone(ctx context.Context, db *sql.DB, id string) error {
	_, err := db.ExecContext(ctx, markRepoDoneSQL, id)
	if err != nil {
		return fmt.Errorf("marking repo done: %w", err)
	}
	return nil
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
