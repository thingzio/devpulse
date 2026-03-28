package tenant

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// Tenant represents a registered SaaS tenant.
type Tenant struct {
	ID            string
	GitHubID      int64
	Username      string
	Email         string
	AvatarURL     string
	MaxRepos      int
	Plan          string
	ToSAcceptedAt *time.Time
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

const upsertTenantSQL = `
	INSERT INTO tenant (github_id, username, email, avatar_url)
	VALUES ($1, $2, $3, $4)
	ON CONFLICT (github_id) DO UPDATE SET
		username = EXCLUDED.username,
		email = EXCLUDED.email,
		avatar_url = EXCLUDED.avatar_url,
		updated_at = NOW()
	RETURNING id, github_id, username, email, avatar_url, max_repos, plan,
	          tos_accepted_at, created_at, updated_at`

const getTenantByGitHubIDSQL = `
	SELECT id, github_id, username, email, avatar_url, max_repos, plan,
	       tos_accepted_at, created_at, updated_at
	FROM tenant WHERE github_id = $1`

const getTenantByIDSQL = `
	SELECT id, github_id, username, email, avatar_url, max_repos, plan,
	       tos_accepted_at, created_at, updated_at
	FROM tenant WHERE id = $1`

const acceptToSSQL = `UPDATE tenant SET tos_accepted_at = NOW(), updated_at = NOW() WHERE id = $1`

const updatePlanSQL = `UPDATE tenant SET plan = $2, max_repos = $3, updated_at = NOW() WHERE id = $1`

func scanTenant(row interface{ Scan(...any) error }) (*Tenant, error) {
	var t Tenant
	err := row.Scan(
		&t.ID, &t.GitHubID, &t.Username, &t.Email, &t.AvatarURL,
		&t.MaxRepos, &t.Plan, &t.ToSAcceptedAt, &t.CreatedAt, &t.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// UpsertTenant creates or updates a tenant by GitHub ID.
func UpsertTenant(ctx context.Context, db *sql.DB, githubID int64, username, email, avatarURL string) (*Tenant, error) {
	t, err := scanTenant(db.QueryRowContext(ctx, upsertTenantSQL, githubID, username, email, avatarURL))
	if err != nil {
		return nil, fmt.Errorf("upserting tenant: %w", err)
	}
	return t, nil
}

// GetTenantByGitHubID returns a tenant by their GitHub user ID.
func GetTenantByGitHubID(ctx context.Context, db *sql.DB, githubID int64) (*Tenant, error) {
	t, err := scanTenant(db.QueryRowContext(ctx, getTenantByGitHubIDSQL, githubID))
	if err != nil {
		return nil, fmt.Errorf("getting tenant by github_id: %w", err)
	}
	return t, nil
}

// GetTenantByID returns a tenant by their internal ID.
func GetTenantByID(ctx context.Context, db *sql.DB, tenantID string) (*Tenant, error) {
	t, err := scanTenant(db.QueryRowContext(ctx, getTenantByIDSQL, tenantID))
	if err != nil {
		return nil, fmt.Errorf("getting tenant by id: %w", err)
	}
	return t, nil
}

// AcceptToS records that a tenant has accepted the Terms of Service.
func AcceptToS(ctx context.Context, db *sql.DB, tenantID string) error {
	_, err := db.ExecContext(ctx, acceptToSSQL, tenantID)
	if err != nil {
		return fmt.Errorf("accepting ToS: %w", err)
	}
	return nil
}

// UpdatePlan sets a tenant's plan and repo limit.
func UpdatePlan(ctx context.Context, db *sql.DB, tenantID, plan string, maxRepos int) error {
	_, err := db.ExecContext(ctx, updatePlanSQL, tenantID, plan, maxRepos)
	if err != nil {
		return fmt.Errorf("updating plan: %w", err)
	}
	return nil
}
