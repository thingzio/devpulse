# Stripe Payment Integration Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Automate Free-to-PRO upgrades via Stripe Checkout with webhook-driven plan management.

**Architecture:** Stripe Checkout Sessions for payment, webhooks for plan state changes, Stripe Customer Portal for self-service billing management. New `pkg/billing/` package wraps Stripe API. Tenant table gains Stripe columns. Frontend gets `/pricing` page and checkout redirect flow.

**Tech Stack:** `github.com/stripe/stripe-go/v82` (Stripe Go SDK), existing Go stdlib HTTP patterns, vanilla JS frontend.

**Design doc:** `docs/plans/2026-03-31-stripe-integration-design.md`

---

### Task 1: Create feature branch

**Step 1: Create and switch to feature branch**

```bash
git checkout -b feat/stripe-integration
```

**Step 2: Verify branch**

```bash
git branch --show-current
```

Expected: `feat/stripe-integration`

---

### Task 2: Add Stripe Go SDK dependency

**Files:**
- Modify: `go.mod`
- Modify: `go.sum`
- Modify: `vendor/`

**Step 1: Add stripe-go dependency**

```bash
go get github.com/stripe/stripe-go/v82
```

**Step 2: Vendor the dependency**

```bash
go mod tidy && go mod vendor
```

**Step 3: Verify it builds**

```bash
go build ./...
```

Expected: clean build, no errors.

**Step 4: Commit**

```bash
git add -A
git commit -S -m "chore: add stripe-go SDK dependency"
```

---

### Task 3: Database migration — add Stripe columns to tenant

**Files:**
- Create: `pkg/data/postgres/sql/migrations_saas/002_stripe_billing.sql`

**Step 1: Write migration**

```sql
-- Add Stripe billing columns to tenant table
ALTER TABLE tenant ADD COLUMN IF NOT EXISTS stripe_customer_id TEXT;
ALTER TABLE tenant ADD COLUMN IF NOT EXISTS stripe_subscription_id TEXT;
ALTER TABLE tenant ADD COLUMN IF NOT EXISTS plan_period_end TIMESTAMPTZ;
ALTER TABLE tenant ADD COLUMN IF NOT EXISTS downgrade_pending BOOLEAN NOT NULL DEFAULT FALSE;

CREATE INDEX IF NOT EXISTS idx_tenant_stripe_customer ON tenant(stripe_customer_id) WHERE stripe_customer_id IS NOT NULL;
```

**Step 2: Verify migration SQL is valid syntax (visual review)**

Confirm: 4 nullable columns + 1 boolean with default + 1 partial index.

**Step 3: Commit**

```bash
git add pkg/data/postgres/sql/migrations_saas/002_stripe_billing.sql
git commit -S -m "feat: add Stripe billing columns to tenant migration"
```

---

### Task 4: Update Tenant struct and all SQL constants

**Files:**
- Modify: `pkg/tenant/tenant.go` (Tenant struct, scanTenant, all SQL constants)
- Modify: `pkg/tenant/session.go` (validateSessionSQL)

This is the critical tenant column checklist from CLAUDE.md.

**Step 1: Update `Tenant` struct** in `pkg/tenant/tenant.go`

Add after `UpgradeRequestedAt`:

```go
StripeCustomerID     *string
StripeSubscriptionID *string
PlanPeriodEnd        *time.Time
DowngradePending     bool
```

**Step 2: Update `scanTenant()`** in `pkg/tenant/tenant.go`

Add to the Scan call after `&t.UpgradeRequestedAt`:

```go
&t.StripeCustomerID, &t.StripeSubscriptionID, &t.PlanPeriodEnd, &t.DowngradePending,
```

**Step 3: Update `upsertTenantSQL`** in `pkg/tenant/tenant.go`

Update the RETURNING clause to include new columns:

```sql
RETURNING id, github_id, username, email, avatar_url, max_repos, max_events_per_week, plan,
          tos_accepted_at, upgrade_requested_at,
          stripe_customer_id, stripe_subscription_id, plan_period_end, downgrade_pending,
          created_at, updated_at
```

**Step 4: Update `getTenantByGitHubIDSQL`** in `pkg/tenant/tenant.go`

Update SELECT to include new columns:

```sql
SELECT id, github_id, username, email, avatar_url, max_repos, max_events_per_week, plan,
       tos_accepted_at, upgrade_requested_at,
       stripe_customer_id, stripe_subscription_id, plan_period_end, downgrade_pending,
       created_at, updated_at
FROM tenant WHERE github_id = $1
```

**Step 5: Update `getTenantByIDSQL`** in `pkg/tenant/tenant.go`

Same column list as Step 4 but `WHERE id = $1`.

**Step 6: Update `validateSessionSQL`** in `pkg/tenant/session.go`

Update the SELECT to include new columns in the same order:

```sql
SELECT t.id, t.github_id, t.username, t.email, t.avatar_url,
       t.max_repos, t.max_events_per_week, t.plan, t.tos_accepted_at, t.upgrade_requested_at,
       t.stripe_customer_id, t.stripe_subscription_id, t.plan_period_end, t.downgrade_pending,
       t.created_at, t.updated_at
FROM session s
JOIN tenant t ON t.id = s.tenant_id
WHERE s.id = $1 AND s.expires_at > NOW()
```

**Step 7: Add new SQL constants** to `pkg/tenant/tenant.go`

```go
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
```

**Step 8: Add new functions** to `pkg/tenant/tenant.go`

```go
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
```

**Step 9: Run tests**

```bash
make test
```

Expected: all existing tests pass (new columns are nullable, no breaking changes).

**Step 10: Run qualify**

```bash
make qualify
```

Expected: clean.

**Step 11: Commit**

```bash
git add pkg/tenant/tenant.go pkg/tenant/session.go
git commit -S -m "feat: add Stripe billing fields to Tenant struct and SQL"
```

---

### Task 5: Update test helpers for new tenant columns

**Files:**
- Modify: `pkg/tenant/tenant_test.go`
- Modify: `pkg/tenant/session_test.go`

**Step 1: Check existing test helpers and update any test assertions**

Review existing tests. The `scanTenant` change adds 4 new fields. If tests use `setupTestDB(t)`, the migration in Task 3 must be applied. Check how migrations are loaded in test setup.

**Step 2: Add test for new tenant functions**

Write tests for `UpdateStripeCustomer`, `UpdateSubscription`, `SetDowngradePending`, `DowngradeToFree`, and `GetTenantByStripeCustomer`. Follow existing table-driven test patterns.

```go
func TestUpdateStripeCustomer(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	tn, err := UpsertTenant(ctx, db, 12345, "testuser", "test@example.com", "https://avatar.url")
	require.NoError(t, err)

	err = UpdateStripeCustomer(ctx, db, tn.ID, "cus_test123")
	require.NoError(t, err)

	got, err := GetTenantByID(ctx, db, tn.ID)
	require.NoError(t, err)
	require.NotNil(t, got.StripeCustomerID)
	assert.Equal(t, "cus_test123", *got.StripeCustomerID)
}

func TestGetTenantByStripeCustomer(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	tn, err := UpsertTenant(ctx, db, 12345, "testuser", "test@example.com", "https://avatar.url")
	require.NoError(t, err)

	err = UpdateStripeCustomer(ctx, db, tn.ID, "cus_test456")
	require.NoError(t, err)

	got, err := GetTenantByStripeCustomer(ctx, db, "cus_test456")
	require.NoError(t, err)
	assert.Equal(t, tn.ID, got.ID)
}

func TestSubscriptionLifecycle(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	tn, err := UpsertTenant(ctx, db, 12345, "testuser", "test@example.com", "https://avatar.url")
	require.NoError(t, err)

	periodEnd := time.Now().Add(30 * 24 * time.Hour).UTC().Truncate(time.Second)

	// Upgrade to pro
	err = UpdateSubscription(ctx, db, tn.ID, "sub_test", "pro", 25, 20000, periodEnd)
	require.NoError(t, err)

	got, err := GetTenantByID(ctx, db, tn.ID)
	require.NoError(t, err)
	assert.Equal(t, "pro", got.Plan)
	assert.Equal(t, 25, got.MaxRepos)
	require.NotNil(t, got.StripeSubscriptionID)
	assert.Equal(t, "sub_test", *got.StripeSubscriptionID)
	assert.False(t, got.DowngradePending)

	// Set downgrade pending
	err = SetDowngradePending(ctx, db, tn.ID, periodEnd)
	require.NoError(t, err)

	got, err = GetTenantByID(ctx, db, tn.ID)
	require.NoError(t, err)
	assert.True(t, got.DowngradePending)

	// Downgrade to free
	err = DowngradeToFree(ctx, db, tn.ID)
	require.NoError(t, err)

	got, err = GetTenantByID(ctx, db, tn.ID)
	require.NoError(t, err)
	assert.Equal(t, "free", got.Plan)
	assert.Equal(t, 5, got.MaxRepos)
	assert.Nil(t, got.StripeSubscriptionID)
	assert.False(t, got.DowngradePending)
}
```

**Step 3: Run tests**

```bash
make test
```

Expected: all pass.

**Step 4: Commit**

```bash
git add pkg/tenant/tenant_test.go
git commit -S -m "test: add Stripe billing tenant function tests"
```

---

### Task 6: Create `pkg/billing/` package — Stripe client wrapper

**Files:**
- Create: `pkg/billing/billing.go`
- Create: `pkg/billing/billing_test.go`

**Step 1: Write `pkg/billing/billing.go`**

```go
package billing

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/stripe/stripe-go/v82"
	"github.com/stripe/stripe-go/v82/billingportal/session"
	checkoutsession "github.com/stripe/stripe-go/v82/checkout/session"
	"github.com/stripe/stripe-go/v82/customer"
)

// Config holds Stripe configuration from environment variables.
type Config struct {
	SecretKey      string
	WebhookSecret  string
	MonthlyPriceID string
	AnnualPriceID  string
	BaseURL        string
}

// LoadConfig reads Stripe config from environment variables.
// Returns nil if STRIPE_SECRET_KEY is not set (Stripe disabled).
func LoadConfig() *Config {
	key := os.Getenv("STRIPE_SECRET_KEY")
	if key == "" {
		return nil
	}
	stripe.Key = key
	return &Config{
		SecretKey:      key,
		WebhookSecret:  os.Getenv("STRIPE_WEBHOOK_SECRET"),
		MonthlyPriceID: os.Getenv("STRIPE_PRO_MONTHLY_PRICE_ID"),
		AnnualPriceID:  os.Getenv("STRIPE_PRO_ANNUAL_PRICE_ID"),
		BaseURL:        os.Getenv("BASE_URL"),
	}
}

// PriceIDForPeriod returns the Stripe Price ID for the given period.
func (c *Config) PriceIDForPeriod(period string) (string, error) {
	switch period {
	case "monthly":
		return c.MonthlyPriceID, nil
	case "annual":
		return c.AnnualPriceID, nil
	default:
		return "", fmt.Errorf("invalid period: %s", period)
	}
}

// CreateCustomer creates a Stripe customer for a tenant.
func CreateCustomer(ctx context.Context, email, tenantID string) (string, error) {
	params := &stripe.CustomerParams{
		Email: stripe.String(email),
	}
	params.Context = ctx
	params.AddMetadata("tenant_id", tenantID)

	c, err := customer.New(params)
	if err != nil {
		return "", fmt.Errorf("creating stripe customer: %w", err)
	}

	slog.Info("stripe customer created", "customer_id", c.ID, "tenant_id", tenantID)
	return c.ID, nil
}

// CreateCheckoutSession creates a Stripe Checkout Session for subscription.
func CreateCheckoutSession(ctx context.Context, cfg *Config, customerID, priceID, tenantID string) (string, error) {
	params := &stripe.CheckoutSessionParams{
		Customer: stripe.String(customerID),
		Mode:     stripe.String(string(stripe.CheckoutSessionModeSubscription)),
		LineItems: []*stripe.CheckoutSessionLineItemParams{
			{
				Price:    stripe.String(priceID),
				Quantity: stripe.Int64(1),
			},
		},
		SuccessURL: stripe.String(cfg.BaseURL + "/pricing?status=success"),
		CancelURL:  stripe.String(cfg.BaseURL + "/pricing?status=cancel"),
	}
	params.Context = ctx
	params.AddMetadata("tenant_id", tenantID)

	s, err := checkoutsession.New(params)
	if err != nil {
		return "", fmt.Errorf("creating checkout session: %w", err)
	}

	slog.Info("checkout session created", "session_id", s.ID, "tenant_id", tenantID)
	return s.URL, nil
}

// CreatePortalSession creates a Stripe Customer Portal session.
func CreatePortalSession(ctx context.Context, cfg *Config, customerID string) (string, error) {
	params := &stripe.BillingPortalSessionParams{
		Customer:  stripe.String(customerID),
		ReturnURL: stripe.String(cfg.BaseURL + "/dashboard"),
	}
	params.Context = ctx

	s, err := session.New(params)
	if err != nil {
		return "", fmt.Errorf("creating portal session: %w", err)
	}
	return s.URL, nil
}

// SubscriptionPeriodEnd extracts the period end from a Stripe subscription.
func SubscriptionPeriodEnd(sub *stripe.Subscription) time.Time {
	return time.Unix(sub.CurrentPeriodEnd, 0).UTC()
}
```

**Step 2: Write unit test for `PriceIDForPeriod`**

```go
package billing

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPriceIDForPeriod(t *testing.T) {
	cfg := &Config{
		MonthlyPriceID: "price_monthly",
		AnnualPriceID:  "price_annual",
	}

	tests := []struct {
		period  string
		want    string
		wantErr bool
	}{
		{"monthly", "price_monthly", false},
		{"annual", "price_annual", false},
		{"weekly", "", true},
		{"", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.period, func(t *testing.T) {
			got, err := cfg.PriceIDForPeriod(tt.period)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}
```

**Step 3: Run tests**

```bash
make test
```

Expected: pass (unit test doesn't call Stripe API).

**Step 4: Commit**

```bash
git add pkg/billing/
git commit -S -m "feat: add pkg/billing with Stripe client wrapper"
```

---

### Task 7: Stripe webhook handler

**Files:**
- Create: `pkg/billing/webhook.go`
- Create: `pkg/billing/webhook_test.go`

**Step 1: Write `pkg/billing/webhook.go`**

```go
package billing

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/stripe/stripe-go/v82"
	"github.com/stripe/stripe-go/v82/webhook"

	"github.com/thingzio/devpulse/pkg/tenant"
)

const maxWebhookBodyBytes = 1 << 16 // 64KB

// Plan limits — must match devpulse-admin planLimits.
var proLimits = [2]int{25, 20000}

// WebhookHandler returns an http.HandlerFunc that processes Stripe webhook events.
func WebhookHandler(db *sql.DB, cfg *Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, maxWebhookBodyBytes))
		if err != nil {
			slog.Error("reading webhook body", "error", err)
			http.Error(w, "error reading body", http.StatusBadRequest)
			return
		}

		event, err := webhook.ConstructEvent(body, r.Header.Get("Stripe-Signature"), cfg.WebhookSecret)
		if err != nil {
			slog.Warn("invalid webhook signature", "error", err)
			http.Error(w, "invalid signature", http.StatusBadRequest)
			return
		}

		ctx := r.Context()
		switch event.Type {
		case "checkout.session.completed":
			handleCheckoutCompleted(ctx, db, event)
		case "customer.subscription.updated":
			handleSubscriptionUpdated(ctx, db, event)
		case "customer.subscription.deleted":
			handleSubscriptionDeleted(ctx, db, event)
		case "invoice.payment_failed":
			handlePaymentFailed(event)
		default:
			slog.Debug("unhandled webhook event", "type", event.Type)
		}

		w.WriteHeader(http.StatusOK)
	}
}

func handleCheckoutCompleted(ctx context.Context, db *sql.DB, event stripe.Event) {
	var session stripe.CheckoutSession
	if err := json.Unmarshal(event.Data.Raw, &session); err != nil {
		slog.Error("unmarshaling checkout session", "error", err)
		return
	}

	if session.Subscription == nil {
		slog.Warn("checkout session has no subscription", "session_id", session.ID)
		return
	}

	customerID := ""
	if session.Customer != nil {
		customerID = session.Customer.ID
	}

	tn, err := tenant.GetTenantByStripeCustomer(ctx, db, customerID)
	if err != nil {
		slog.Error("finding tenant for checkout", "customer_id", customerID, "error", err)
		return
	}

	periodEnd := time.Unix(session.Subscription.CurrentPeriodEnd, 0).UTC()
	if err := tenant.UpdateSubscription(ctx, db, tn.ID, session.Subscription.ID, "pro", proLimits[0], proLimits[1], periodEnd); err != nil {
		slog.Error("updating subscription after checkout", "tenant_id", tn.ID, "error", err)
		return
	}

	slog.Info("tenant upgraded via checkout",
		"tenant_id", tn.ID,
		"username", tn.Username,
		"subscription_id", session.Subscription.ID,
	)
}

func handleSubscriptionUpdated(ctx context.Context, db *sql.DB, event stripe.Event) {
	var sub stripe.Subscription
	if err := json.Unmarshal(event.Data.Raw, &sub); err != nil {
		slog.Error("unmarshaling subscription", "error", err)
		return
	}

	customerID := ""
	if sub.Customer != nil {
		customerID = sub.Customer.ID
	}

	tn, err := tenant.GetTenantByStripeCustomer(ctx, db, customerID)
	if err != nil {
		slog.Error("finding tenant for subscription update", "customer_id", customerID, "error", err)
		return
	}

	periodEnd := time.Unix(sub.CurrentPeriodEnd, 0).UTC()
	if err := tenant.UpdateSubscription(ctx, db, tn.ID, sub.ID, "pro", proLimits[0], proLimits[1], periodEnd); err != nil {
		slog.Error("updating subscription", "tenant_id", tn.ID, "error", err)
		return
	}

	slog.Info("subscription updated", "tenant_id", tn.ID, "subscription_id", sub.ID)
}

func handleSubscriptionDeleted(ctx context.Context, db *sql.DB, event stripe.Event) {
	var sub stripe.Subscription
	if err := json.Unmarshal(event.Data.Raw, &sub); err != nil {
		slog.Error("unmarshaling subscription", "error", err)
		return
	}

	customerID := ""
	if sub.Customer != nil {
		customerID = sub.Customer.ID
	}

	tn, err := tenant.GetTenantByStripeCustomer(ctx, db, customerID)
	if err != nil {
		slog.Error("finding tenant for subscription deletion", "customer_id", customerID, "error", err)
		return
	}

	// Check active repo count — if within free limits, downgrade immediately
	activeRepos, err := tenant.GetActiveRepoCount(ctx, db, tn.ID)
	if err != nil {
		slog.Error("checking active repos for downgrade", "tenant_id", tn.ID, "error", err)
		return
	}

	if activeRepos <= 5 {
		if err := tenant.DowngradeToFree(ctx, db, tn.ID); err != nil {
			slog.Error("downgrading tenant", "tenant_id", tn.ID, "error", err)
			return
		}
		slog.Info("tenant downgraded to free", "tenant_id", tn.ID, "username", tn.Username)
	} else {
		periodEnd := time.Unix(sub.CurrentPeriodEnd, 0).UTC()
		if err := tenant.SetDowngradePending(ctx, db, tn.ID, periodEnd); err != nil {
			slog.Error("setting downgrade pending", "tenant_id", tn.ID, "error", err)
			return
		}
		slog.Info("downgrade pending — repos exceed free limit",
			"tenant_id", tn.ID,
			"username", tn.Username,
			"active_repos", activeRepos,
		)
	}
}

func handlePaymentFailed(event stripe.Event) {
	var invoice stripe.Invoice
	if err := json.Unmarshal(event.Data.Raw, &invoice); err != nil {
		slog.Error("unmarshaling invoice", "error", err)
		return
	}

	customerID := ""
	if invoice.Customer != nil {
		customerID = invoice.Customer.ID
	}

	slog.Warn("payment failed",
		"customer_id", customerID,
		"invoice_id", invoice.ID,
		"attempt_count", invoice.AttemptCount,
	)
}
```

**Step 2: Add `GetActiveRepoCount` to `pkg/tenant/overview.go`**

This function already has the SQL (`tenantActiveRepoCountSQL`), just needs a public wrapper:

```go
func GetActiveRepoCount(ctx context.Context, db *sql.DB, tenantID string) (int, error) {
	var count int
	if err := db.QueryRowContext(ctx, tenantActiveRepoCountSQL, tenantID).Scan(&count); err != nil {
		return 0, fmt.Errorf("counting active repos: %w", err)
	}
	return count, nil
}
```

**Step 3: Write webhook signature verification test**

```go
package billing

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestWebhookHandler_InvalidSignature(t *testing.T) {
	cfg := &Config{WebhookSecret: "whsec_test"}
	handler := WebhookHandler(nil, cfg)

	// Test with invalid signature — should return 400
	req := httptest.NewRequest(http.MethodPost, "/webhooks/stripe", strings.NewReader("{}"))
	req.Header.Set("Stripe-Signature", "invalid")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}
```

Add necessary imports: `net/http`, `net/http/httptest`, `strings`.

**Step 4: Run tests**

```bash
make test
```

**Step 5: Run qualify**

```bash
make qualify
```

**Step 6: Commit**

```bash
git add pkg/billing/webhook.go pkg/billing/webhook_test.go pkg/tenant/overview.go
git commit -S -m "feat: add Stripe webhook handler with plan lifecycle management"
```

---

### Task 8: Checkout and portal HTTP handlers

**Files:**
- Create: `pkg/server/billing.go`

**Step 1: Write `pkg/server/billing.go`**

```go
package server

import (
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/thingzio/devpulse/pkg/billing"
	"github.com/thingzio/devpulse/pkg/middleware"
	"github.com/thingzio/devpulse/pkg/tenant"
)

type checkoutRequest struct {
	Period string `json:"period"`
}

func checkoutHandler(db *sql.DB, cfg *billing.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tn := middleware.TenantFromContext(r.Context())
		if tn == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		if tn.Plan == "pro" {
			http.Error(w, "already on pro plan", http.StatusBadRequest)
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		var req checkoutRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}

		priceID, err := cfg.PriceIDForPeriod(req.Period)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		// Create or reuse Stripe customer
		customerID := ""
		if tn.StripeCustomerID != nil {
			customerID = *tn.StripeCustomerID
		} else {
			customerID, err = billing.CreateCustomer(r.Context(), tn.Email, tn.ID)
			if err != nil {
				slog.Error("creating stripe customer", "error", err)
				http.Error(w, "error creating customer", http.StatusInternalServerError)
				return
			}
			if err := tenant.UpdateStripeCustomer(r.Context(), db, tn.ID, customerID); err != nil {
				slog.Error("saving stripe customer id", "error", err)
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
		}

		url, err := billing.CreateCheckoutSession(r.Context(), cfg, customerID, priceID, tn.ID)
		if err != nil {
			slog.Error("creating checkout session", "error", err)
			http.Error(w, "error creating checkout session", http.StatusInternalServerError)
			return
		}

		writeJSON(w, http.StatusOK, map[string]string{"url": url})
	}
}

func billingPortalHandler(db *sql.DB, cfg *billing.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tn := middleware.TenantFromContext(r.Context())
		if tn == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		if tn.StripeCustomerID == nil {
			http.Error(w, "no billing account", http.StatusBadRequest)
			return
		}

		url, err := billing.CreatePortalSession(r.Context(), cfg, *tn.StripeCustomerID)
		if err != nil {
			slog.Error("creating portal session", "error", err)
			http.Error(w, "error creating portal session", http.StatusInternalServerError)
			return
		}

		http.Redirect(w, r, url, http.StatusFound)
	}
}
```

**Step 2: Run build to verify compilation**

```bash
go build ./pkg/server/...
```

**Step 3: Commit**

```bash
git add pkg/server/billing.go
git commit -S -m "feat: add checkout and billing portal HTTP handlers"
```

---

### Task 9: Wire routes into the server

**Files:**
- Modify: `pkg/server/server.go`

**Step 1: Update `Run()` to load Stripe config**

In `Run()`, after `webhookSecret :=` line, add:

```go
stripeCfg := billing.LoadConfig()
if stripeCfg != nil {
	slog.Info("stripe billing enabled")
} else {
	slog.Info("stripe billing disabled (STRIPE_SECRET_KEY not set)")
}
```

Add import for `"github.com/thingzio/devpulse/pkg/billing"`.

**Step 2: Pass `stripeCfg` to `makeRouter`**

Update `makeRouter` signature to accept `*billing.Config`:

```go
func makeRouter(db *sql.DB, store data.Store, oauthCfg *oauth.Config, webhookSecret string, stripeCfg *billing.Config, opts Options) *http.ServeMux {
```

Update the call in `Run()`:

```go
mux := makeRouter(db, store, oauthCfg, webhookSecret, stripeCfg, opts)
```

**Step 3: Add routes inside `makeRouter`**

After the existing `POST /webhook/github` line, add:

```go
// Stripe webhook (public, verified by signature)
if stripeCfg != nil {
	mux.HandleFunc("POST /webhooks/stripe", billing.WebhookHandler(db, stripeCfg))
}
```

In the authenticated routes section, after `POST /api/upgrade-request`, add:

```go
// Billing routes (only if Stripe is configured)
if stripeCfg != nil {
	mux.Handle("POST /api/checkout", wrap(checkoutHandler(db, stripeCfg)))
	mux.Handle("GET /api/billing/portal", wrap(billingPortalHandler(db, stripeCfg)))
}
```

Add the pricing page route in the public routes section:

```go
mux.HandleFunc("GET /pricing", func(w http.ResponseWriter, _ *http.Request) {
	renderTemplate(w, "pricing.html", pageData{Title: "Pricing"})
})
```

**Step 4: Register `pricing.html` template** in `init()`

Update simplePages:

```go
simplePages := []string{"landing.html", "tos.html", "help.html", "pricing.html"}
```

**Step 5: Run build**

```bash
go build ./pkg/server/...
```

Note: will fail until pricing template exists (Task 10).

**Step 6: Commit**

```bash
git add pkg/server/server.go
git commit -S -m "feat: wire Stripe routes into server"
```

---

### Task 10: Pricing page template

**Files:**
- Create: `pkg/server/templates/pricing.html`

**Step 1: Write pricing template**

Create `pkg/server/templates/pricing.html` following the existing `layout.html` pattern (extends layout with `define "content"`). Include:

- Free tier card: 5 repos, 2K events/week, community features
- PRO tier card: 25 repos, 20K events/week, all features
- Monthly/Annual toggle
- "Get Started" button for Free (links to `/auth/github`)
- "Subscribe" button for PRO (calls `POST /api/checkout`)
- Success/cancel status messaging from URL params

Use existing CSS variables and patterns from `landing.html`. Keep it simple — cards with feature lists, a period toggle, and CTA buttons.

The JS for checkout redirect:

```javascript
document.querySelectorAll('.subscribe-btn').forEach(function(btn) {
    btn.addEventListener('click', function() {
        var period = document.querySelector('input[name="period"]:checked').value;
        fetch('/api/checkout', {
            method: 'POST',
            headers: {'Content-Type': 'application/json'},
            body: JSON.stringify({period: period})
        })
        .then(function(r) { return r.json(); })
        .then(function(data) { window.location.href = data.url; })
        .catch(function() { alert('Error starting checkout. Please try again.'); });
    });
});
```

**Step 2: Run build**

```bash
go build ./...
```

Expected: builds successfully.

**Step 3: Commit**

```bash
git add pkg/server/templates/pricing.html
git commit -S -m "feat: add pricing page template"
```

---

### Task 11: Update dashboard JS for Stripe checkout flow

**Files:**
- Modify: `pkg/server/static/js/app.js`

**Step 1: Update the upgrade prompt in app.js**

Find the existing `request-upgrade-link` handler (around line 2350) and update it to redirect to the pricing page instead of posting to `/api/upgrade-request`:

```javascript
// Replace the existing upgrade request click handler
$('#request-upgrade-link').on('click', function(e) {
    e.preventDefault();
    window.location.href = '/pricing';
});
```

**Step 2: Add "Manage Billing" link to dashboard banner**

In the banner rendering section (around line 1517), add a billing link for PRO users:

```javascript
if ((u.plan || 'free') === 'pro') {
    // Add manage billing link
}
```

**Step 3: Add downgrade pending banner**

If `u.downgrade_pending` is true, show a warning banner:

```javascript
if (u.downgrade_pending) {
    $banner.append('<div class="banner-warn">Downgrade pending. Remove repos to ' +
        (u.max_repos) + ' or fewer to complete.</div>');
}
```

**Step 4: Run build and verify**

```bash
go build ./...
```

**Step 5: Commit**

```bash
git add pkg/server/static/js/app.js
git commit -S -m "feat: update dashboard for Stripe checkout redirect and billing links"
```

---

### Task 12: Update overview API to include billing fields

**Files:**
- Modify: `pkg/tenant/overview.go`

**Step 1: Add billing fields to `UsageSummary`**

```go
type UsageSummary struct {
	Plan             string  `json:"plan"`
	MaxRepos         int     `json:"max_repos"`
	ActiveRepos      int     `json:"active_repos"`
	MaxEventsPerWeek int     `json:"max_events_per_week"`
	WeeklyEvents     int     `json:"weekly_events"`
	WeeklyPct        float64 `json:"weekly_pct"`
	LimitReached     bool    `json:"limit_reached"`
	DowngradePending bool    `json:"downgrade_pending"`
	HasBilling       bool    `json:"has_billing"`
}
```

**Step 2: Update `GetOverview` to populate new fields**

After querying tenant limits, also query `downgrade_pending` and `stripe_customer_id`:

Update `tenantLimitsSQL`:

```sql
SELECT plan, max_repos, max_events_per_week, downgrade_pending,
       stripe_customer_id IS NOT NULL
FROM tenant WHERE id = $1
```

Add scan variables:

```go
var downgradePending, hasBilling bool
```

Update Scan:

```go
.Scan(&plan, &maxRepos, &maxEventsPerWeek, &downgradePending, &hasBilling)
```

Set in `UsageSummary`:

```go
DowngradePending: downgradePending,
HasBilling:       hasBilling,
```

**Step 3: Run tests**

```bash
make test
```

**Step 4: Commit**

```bash
git add pkg/tenant/overview.go
git commit -S -m "feat: include billing status in overview API response"
```

---

### Task 13: Update Terraform for Stripe env vars

**Files:**
- Modify: `infra/saas/variables.tf` (add Stripe variables)
- Modify: `infra/saas/main.tf` (add secrets, pass to Cloud Run)

**Step 1: Add Terraform variables**

```hcl
variable "stripe_publishable_key" {
  description = "Stripe publishable key"
  type        = string
  default     = ""
}

variable "stripe_pro_monthly_price_id" {
  description = "Stripe PRO monthly price ID"
  type        = string
  default     = ""
}

variable "stripe_pro_annual_price_id" {
  description = "Stripe PRO annual price ID"
  type        = string
  default     = ""
}
```

**Step 2: Add Secret Manager secrets for Stripe keys**

Follow existing pattern for `github_oauth_client_secret`:
- `stripe_secret_key` in Secret Manager
- `stripe_webhook_secret` in Secret Manager

**Step 3: Add env vars to Cloud Run service template**

Add to the `devpulse-site` container env section:
- `STRIPE_SECRET_KEY` from secret
- `STRIPE_WEBHOOK_SECRET` from secret
- `STRIPE_PRO_MONTHLY_PRICE_ID` from variable
- `STRIPE_PRO_ANNUAL_PRICE_ID` from variable

**Step 4: Commit**

```bash
git add infra/saas/
git commit -S -m "feat: add Stripe env vars to Terraform config"
```

---

### Task 14: Final integration test and qualify

**Step 1: Run full qualify**

```bash
make qualify
```

Expected: all tests pass, no lint errors, no vulnerabilities.

**Step 2: Manual review checklist**

- [ ] All 5 tenant SQL constants updated with new columns
- [ ] `scanTenant()` field order matches SQL column order
- [ ] `validateSessionSQL` includes new columns
- [ ] Webhook handler verifies Stripe signature
- [ ] Checkout handler creates customer before session
- [ ] Downgrade flow checks repo count
- [ ] Pricing page works without auth
- [ ] Stripe routes only register when config is present

**Step 3: Final commit if any fixes needed**

```bash
git add -A
git commit -S -m "fix: address qualify issues"
```

---

## Unresolved Questions

1. **Stripe test mode vs production** — Do you want to set up Stripe test mode first for local dev, or go straight to production setup?
2. **Email notifications** — Should devpulse send email on upgrade/downgrade, or rely solely on Stripe's built-in receipt emails?
3. **Pricing amounts** — What are the actual monthly and annual prices for PRO?
4. **Downgrade grace period** — After `plan_period_end` passes and repos are still over limit, should imports stop entirely or continue with degraded service?
5. **Admin service update** — Should `devpulse-admin` also be updated to understand Stripe fields, or keep it as-is for manual overrides?
