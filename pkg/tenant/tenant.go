package tenant

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// Tenant represents a registered SaaS tenant.
type Tenant struct {
	ID                   string
	GitHubID             int64
	Username             string
	Email                string
	AvatarURL            string
	MaxRepos             int
	MaxEventsPerWeek     int
	Plan                 string
	ToSAcceptedAt        *time.Time
	UpgradeRequestedAt   *time.Time
	StripeCustomerID     *string
	StripeSubscriptionID *string
	PlanPeriodEnd        *time.Time
	DowngradePending     bool
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

const upsertTenantSQL = `
	INSERT INTO tenant (github_id, username, email, avatar_url)
	VALUES ($1, $2, $3, $4)
	ON CONFLICT (github_id) DO UPDATE SET
		username = EXCLUDED.username,
		email = EXCLUDED.email,
		avatar_url = EXCLUDED.avatar_url,
		updated_at = NOW()
	RETURNING id, github_id, username, email, avatar_url, max_repos, max_events_per_week, plan,
	          tos_accepted_at, upgrade_requested_at,
	          stripe_customer_id, stripe_subscription_id, plan_period_end, downgrade_pending,
	          created_at, updated_at`

const getTenantByGitHubIDSQL = `
	SELECT id, github_id, username, email, avatar_url, max_repos, max_events_per_week, plan,
	       tos_accepted_at, upgrade_requested_at,
	       stripe_customer_id, stripe_subscription_id, plan_period_end, downgrade_pending,
	       created_at, updated_at
	FROM tenant WHERE github_id = $1`

const getTenantByIDSQL = `
	SELECT id, github_id, username, email, avatar_url, max_repos, max_events_per_week, plan,
	       tos_accepted_at, upgrade_requested_at,
	       stripe_customer_id, stripe_subscription_id, plan_period_end, downgrade_pending,
	       created_at, updated_at
	FROM tenant WHERE id = $1`

const acceptToSSQL = `UPDATE tenant SET tos_accepted_at = NOW(), updated_at = NOW() WHERE id = $1`

const updatePlanSQL = `UPDATE tenant SET plan = $2, max_repos = $3, max_events_per_week = $4, updated_at = NOW() WHERE id = $1`

const updateStripeCustomerSQL = `UPDATE tenant SET stripe_customer_id = $2, updated_at = NOW() WHERE id = $1`

const updateSubscriptionSQL = `
	UPDATE tenant SET stripe_subscription_id = $2, plan = $3, max_repos = $4,
	       max_events_per_week = $5, plan_period_end = $6, downgrade_pending = FALSE, updated_at = NOW()
	WHERE id = $1`

const clearSubscriptionSQL = `
	UPDATE tenant SET stripe_subscription_id = NULL, plan_period_end = $2, updated_at = NOW()
	WHERE id = $1`

const setDowngradePendingSQL = `
	UPDATE tenant SET downgrade_pending = TRUE, plan_period_end = $2, updated_at = NOW()
	WHERE id = $1`

const downgradeToFreeSQL = `
	UPDATE tenant SET plan = 'free', max_repos = 5, max_events_per_week = 2000,
	       stripe_subscription_id = NULL, plan_period_end = NULL, downgrade_pending = FALSE, updated_at = NOW()
	WHERE id = $1`

const getTenantByStripeCustomerSQL = `
	SELECT id, github_id, username, email, avatar_url, max_repos, max_events_per_week, plan,
	       tos_accepted_at, upgrade_requested_at,
	       stripe_customer_id, stripe_subscription_id, plan_period_end, downgrade_pending,
	       created_at, updated_at
	FROM tenant WHERE stripe_customer_id = $1`

const requestUpgradeSQL = `
	UPDATE tenant SET upgrade_requested_at = NOW(), updated_at = NOW()
	WHERE id = $1 AND upgrade_requested_at IS NULL
	RETURNING username, email, plan, max_repos`

func scanTenant(row interface{ Scan(...any) error }) (*Tenant, error) {
	var t Tenant
	err := row.Scan(
		&t.ID, &t.GitHubID, &t.Username, &t.Email, &t.AvatarURL,
		&t.MaxRepos, &t.MaxEventsPerWeek, &t.Plan, &t.ToSAcceptedAt, &t.UpgradeRequestedAt,
		&t.StripeCustomerID, &t.StripeSubscriptionID, &t.PlanPeriodEnd, &t.DowngradePending,
		&t.CreatedAt, &t.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("scanning tenant: %w", err)
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
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("requesting upgrade: %w", err)
	}
	return &req, nil
}

func UpdateStripeCustomer(ctx context.Context, db *sql.DB, tenantID, customerID string) error {
	_, err := db.ExecContext(ctx, updateStripeCustomerSQL, tenantID, customerID)
	if err != nil {
		return fmt.Errorf("updating stripe customer: %w", err)
	}
	return nil
}

func UpdateSubscription(ctx context.Context, db *sql.DB, tenantID, subID, plan string, maxRepos, maxEvents int, periodEnd time.Time) error {
	_, err := db.ExecContext(ctx, updateSubscriptionSQL, tenantID, subID, plan, maxRepos, maxEvents, periodEnd)
	if err != nil {
		return fmt.Errorf("updating subscription: %w", err)
	}
	return nil
}

func ClearSubscription(ctx context.Context, db *sql.DB, tenantID string, periodEnd time.Time) error {
	_, err := db.ExecContext(ctx, clearSubscriptionSQL, tenantID, periodEnd)
	if err != nil {
		return fmt.Errorf("clearing subscription: %w", err)
	}
	return nil
}

func SetDowngradePending(ctx context.Context, db *sql.DB, tenantID string, periodEnd time.Time) error {
	_, err := db.ExecContext(ctx, setDowngradePendingSQL, tenantID, periodEnd)
	if err != nil {
		return fmt.Errorf("setting downgrade pending: %w", err)
	}
	return nil
}

func DowngradeToFree(ctx context.Context, db *sql.DB, tenantID string) error {
	_, err := db.ExecContext(ctx, downgradeToFreeSQL, tenantID)
	if err != nil {
		return fmt.Errorf("downgrading to free: %w", err)
	}
	return nil
}

func GetTenantByStripeCustomer(ctx context.Context, db *sql.DB, customerID string) (*Tenant, error) {
	t, err := scanTenant(db.QueryRowContext(ctx, getTenantByStripeCustomerSQL, customerID))
	if err != nil {
		return nil, fmt.Errorf("getting tenant by stripe customer: %w", err)
	}
	return t, nil
}
