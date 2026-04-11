package tenant

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Tenant represents a registered SaaS tenant.
type Tenant struct {
	ID                 string
	GitHubID           int64
	Username           string
	Email              string
	AvatarURL          string
	Name               string
	Company            string
	Location           string
	Bio                string
	MaxRepos           int
	MaxEventsPerWeek   int
	Plan               string
	ToSAcceptedAt      *time.Time
	UpgradeRequestedAt *time.Time
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

const upsertTenantSQL = `
	INSERT INTO tenant (github_id, username, email, avatar_url, name, company, location, bio)
	VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	ON CONFLICT (github_id) DO UPDATE SET
		username = EXCLUDED.username,
		email = EXCLUDED.email,
		avatar_url = EXCLUDED.avatar_url,
		name = EXCLUDED.name,
		company = EXCLUDED.company,
		location = EXCLUDED.location,
		bio = EXCLUDED.bio,
		updated_at = NOW()
	RETURNING id, github_id, username, email, avatar_url, name, company, location, bio,
	          max_repos, max_events_per_week, plan,
	          tos_accepted_at, upgrade_requested_at, created_at, updated_at`

const getTenantByGitHubIDSQL = `
	SELECT id, github_id, username, email, avatar_url, name, company, location, bio,
	       max_repos, max_events_per_week, plan,
	       tos_accepted_at, upgrade_requested_at, created_at, updated_at
	FROM tenant WHERE github_id = $1`

const getTenantByIDSQL = `
	SELECT id, github_id, username, email, avatar_url, name, company, location, bio,
	       max_repos, max_events_per_week, plan,
	       tos_accepted_at, upgrade_requested_at, created_at, updated_at
	FROM tenant WHERE id = $1`

const acceptToSSQL = `UPDATE tenant SET tos_accepted_at = NOW(), updated_at = NOW() WHERE id = $1`

const updatePlanSQL = `UPDATE tenant SET plan = $2, max_repos = $3, max_events_per_week = $4, updated_at = NOW() WHERE id = $1`

const requestUpgradeSQL = `
	UPDATE tenant SET upgrade_requested_at = NOW(), updated_at = NOW()
	WHERE id = $1 AND upgrade_requested_at IS NULL
	RETURNING username, email, plan, max_repos`

func scanTenant(row interface{ Scan(...any) error }) (*Tenant, error) {
	var t Tenant
	err := row.Scan(
		&t.ID, &t.GitHubID, &t.Username, &t.Email, &t.AvatarURL,
		&t.Name, &t.Company, &t.Location, &t.Bio,
		&t.MaxRepos, &t.MaxEventsPerWeek, &t.Plan, &t.ToSAcceptedAt, &t.UpgradeRequestedAt, &t.CreatedAt, &t.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("scanning tenant: %w", err)
	}
	return &t, nil
}

// UpsertTenant creates or updates a tenant by GitHub ID.
func UpsertTenant(ctx context.Context, db *sql.DB, githubID int64, username, email, avatarURL, name, company, location, bio string) (*Tenant, error) {
	t, err := scanTenant(db.QueryRowContext(ctx, upsertTenantSQL, githubID, username, email, avatarURL, name, company, location, bio))
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

// UpdatePlan sets a tenant's plan, repo limit, and event limit.
func UpdatePlan(ctx context.Context, db *sql.DB, tenantID, plan string, maxRepos, maxEventsPerWeek int) error {
	_, err := db.ExecContext(ctx, updatePlanSQL, tenantID, plan, maxRepos, maxEventsPerWeek)
	if err != nil {
		return fmt.Errorf("updating plan: %w", err)
	}
	return nil
}

// UpgradeRequest holds details returned when an upgrade is first requested.
type UpgradeRequest struct {
	Username string
	Email    string
	Plan     string
	MaxRepos int
}

// RequestUpgrade idempotently records an upgrade request for a tenant.
// Returns the request details on first call, nil on subsequent calls.
func RequestUpgrade(ctx context.Context, db *sql.DB, tenantID string) (*UpgradeRequest, error) {
	var req UpgradeRequest
	err := db.QueryRowContext(ctx, requestUpgradeSQL, tenantID).Scan(
		&req.Username, &req.Email, &req.Plan, &req.MaxRepos,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("requesting upgrade: %w", err)
	}
	return &req, nil
}
