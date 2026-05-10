package tenant

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/thingzio/devpulse/pkg/plan"
)

// Tenant status values.
const (
	StatusActive    = "active"
	StatusSuspended = "suspended"
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
	Status             string
	WeeklyDigest       bool
	ToSAcceptedAt      *time.Time
	UpgradeRequestedAt *time.Time
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// upsertTenantSQL inserts a new tenant with the Pro plan defaults, or updates
// an existing tenant's profile fields. Plan/limits are set on INSERT only —
// existing tenants keep whatever plan they currently have.
const upsertTenantSQL = `
	INSERT INTO devpulse_tenant (github_id, username, email, avatar_url, name, company, location, bio, plan, max_repos, max_events_per_week)
	VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
	ON CONFLICT (github_id) DO UPDATE SET
		username = EXCLUDED.username,
		email = EXCLUDED.email,
		avatar_url = EXCLUDED.avatar_url,
		name = EXCLUDED.name,
		company = EXCLUDED.company,
		location = EXCLUDED.location,
		bio = EXCLUDED.bio,
		updated_at = NOW()
	RETURNING id, github_id, username, COALESCE(email, ''), COALESCE(avatar_url, ''),
		          COALESCE(name, ''), COALESCE(company, ''), COALESCE(location, ''), COALESCE(bio, ''),
	          max_repos, max_events_per_week, plan, status, weekly_digest,
	          tos_accepted_at, upgrade_requested_at, created_at, updated_at`

const getTenantByGitHubIDSQL = `
	SELECT id, github_id, username, COALESCE(email, ''), COALESCE(avatar_url, ''),
	       COALESCE(name, ''), COALESCE(company, ''), COALESCE(location, ''), COALESCE(bio, ''),
	       max_repos, max_events_per_week, plan, status, weekly_digest,
	       tos_accepted_at, upgrade_requested_at, created_at, updated_at
	FROM devpulse_tenant WHERE github_id = $1`

const getTenantByIDSQL = `
	SELECT id, github_id, username, COALESCE(email, ''), COALESCE(avatar_url, ''),
	       COALESCE(name, ''), COALESCE(company, ''), COALESCE(location, ''), COALESCE(bio, ''),
	       max_repos, max_events_per_week, plan, status, weekly_digest,
	       tos_accepted_at, upgrade_requested_at, created_at, updated_at
	FROM devpulse_tenant WHERE id = $1`

const acceptToSSQL = `UPDATE devpulse_tenant SET tos_accepted_at = NOW(), updated_at = NOW() WHERE id = $1`

const updatePlanSQL = `UPDATE devpulse_tenant SET plan = $2, max_repos = $3, max_events_per_week = $4, updated_at = NOW() WHERE id = $1`

const updateStatusSQL = `UPDATE devpulse_tenant SET status = $2, updated_at = NOW() WHERE id = $1`

const updateWeeklyDigestSQL = `UPDATE devpulse_tenant SET weekly_digest = $2, updated_at = NOW() WHERE id = $1`

const updateDigestLastSentSQL = `UPDATE devpulse_tenant SET digest_last_sent_at = NOW(), updated_at = NOW() WHERE id = $1`

const listDigestTenantsSQL = `
	SELECT id, username, email, digest_last_sent_at FROM devpulse_tenant
	WHERE status = 'active' AND weekly_digest = TRUE AND COALESCE(email, '') != ''`

const requestUpgradeSQL = `
	UPDATE devpulse_tenant SET upgrade_requested_at = NOW(), updated_at = NOW()
	WHERE id = $1 AND upgrade_requested_at IS NULL
	RETURNING username, email, plan, max_repos`

func scanTenant(row interface{ Scan(...any) error }) (*Tenant, error) {
	var t Tenant
	err := row.Scan(
		&t.ID, &t.GitHubID, &t.Username, &t.Email, &t.AvatarURL,
		&t.Name, &t.Company, &t.Location, &t.Bio,
		&t.MaxRepos, &t.MaxEventsPerWeek, &t.Plan, &t.Status, &t.WeeklyDigest,
		&t.ToSAcceptedAt, &t.UpgradeRequestedAt, &t.CreatedAt, &t.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("scanning tenant: %w", err)
	}
	return &t, nil
}

// UpsertTenant creates or updates a tenant by GitHub ID. New tenants are
// auto-enrolled in the Pro plan during the beta preview; existing tenants
// keep their current plan.
func UpsertTenant(ctx context.Context, db *sql.DB, githubID int64, username, email, avatarURL, name, company, location, bio string) (*Tenant, error) {
	pro, _ := plan.Get(plan.Pro)
	t, err := scanTenant(db.QueryRowContext(ctx, upsertTenantSQL,
		githubID, username, email, avatarURL, name, company, location, bio,
		plan.Pro, pro.MaxRepos, pro.MaxEventsPerWeek))
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

// UpdateStatus sets the tenant status (e.g. "active", "suspended").
func UpdateStatus(ctx context.Context, db *sql.DB, tenantID, status string) error {
	_, err := db.ExecContext(ctx, updateStatusSQL, tenantID, status)
	if err != nil {
		return fmt.Errorf("updating status: %w", err)
	}
	return nil
}

// UpdateWeeklyDigest toggles the weekly digest preference for a tenant.
func UpdateWeeklyDigest(ctx context.Context, db *sql.DB, tenantID string, enabled bool) error {
	_, err := db.ExecContext(ctx, updateWeeklyDigestSQL, tenantID, enabled)
	if err != nil {
		return fmt.Errorf("updating weekly digest: %w", err)
	}
	return nil
}

// UpdateDigestLastSent records when a digest email was last sent to a tenant.
func UpdateDigestLastSent(ctx context.Context, db *sql.DB, tenantID string) error {
	_, err := db.ExecContext(ctx, updateDigestLastSentSQL, tenantID)
	if err != nil {
		return fmt.Errorf("updating digest last sent: %w", err)
	}
	return nil
}

// DigestTenant is a lightweight record for digest-eligible tenants.
type DigestTenant struct {
	ID         string
	Username   string
	Email      string
	LastSentAt *time.Time
}

// ListDigestTenants returns all active tenants opted into the weekly digest
// that have a non-empty email address.
func ListDigestTenants(ctx context.Context, db *sql.DB) ([]DigestTenant, error) {
	rows, err := db.QueryContext(ctx, listDigestTenantsSQL)
	if err != nil {
		return nil, fmt.Errorf("listing digest tenants: %w", err)
	}
	defer rows.Close()

	var out []DigestTenant
	for rows.Next() {
		var dt DigestTenant
		var lastSent sql.NullTime
		if err := rows.Scan(&dt.ID, &dt.Username, &dt.Email, &lastSent); err != nil {
			return nil, fmt.Errorf("scanning digest tenant: %w", err)
		}
		if lastSent.Valid {
			dt.LastSentAt = &lastSent.Time
		}
		out = append(out, dt)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating digest tenants: %w", err)
	}
	return out, nil
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
