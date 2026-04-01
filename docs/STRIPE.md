# Stripe Billing Integration

Automated Free-to-PRO upgrades via Stripe Checkout with webhook-driven plan management. Enterprise tier remains sales-led.

## Architecture

```
User clicks Subscribe
  -> POST /api/checkout (creates Stripe Checkout Session)
  -> Redirect to Stripe-hosted payment page
  -> Stripe webhook fires checkout.session.completed
  -> Backend upgrades tenant to PRO

User manages billing
  -> GET /api/billing/portal (creates Stripe Customer Portal session)
  -> Redirect to Stripe-hosted portal (cancel, switch plan, update payment)
  -> Stripe webhook fires subscription events
  -> Backend updates tenant plan accordingly
```

## Plans

| Plan | Repos | Events/Week | Price | Managed By |
|------|-------|-------------|-------|------------|
| Free | 5 | 2,000 | $0 | Default |
| PRO | 25 | 20,000 | $19/mo or $190/yr | Stripe |
| Enterprise | 100 | 100,000 | Custom | Sales (manual) |

Prices are defined in the Stripe Dashboard. The app references them by Price ID via environment variables. Display prices are hardcoded in `pkg/server/templates/pricing.html` — update both places when changing prices.

## Environment Variables

| Variable | Storage | Required | Purpose |
|----------|---------|----------|---------|
| `STRIPE_SECRET_KEY` | Secret Manager | Yes | Server-side API key |
| `STRIPE_WEBHOOK_SECRET` | Secret Manager | Yes | Webhook HMAC signature verification |
| `STRIPE_PRO_MONTHLY_PRICE_ID` | Env var / TF variable | Yes | Stripe Price ID for monthly plan |
| `STRIPE_PRO_ANNUAL_PRICE_ID` | Env var / TF variable | Yes | Stripe Price ID for annual plan |

When `STRIPE_SECRET_KEY` is not set, all Stripe routes are disabled and the app functions as before (manual upgrades only).

## API Endpoints

| Method | Path | Auth | Purpose |
|--------|------|------|---------|
| `POST` | `/api/checkout` | Session cookie | Create Checkout Session, return `{"url": "..."}` for redirect. Body: `{"period": "monthly"\|"annual"}` |
| `GET` | `/api/billing/portal` | Session cookie | Create Customer Portal session, 302 redirect |
| `POST` | `/webhooks/stripe` | Stripe signature | Receive webhook events, update tenant plan |
| `GET` | `/pricing` | Public | Pricing page with Free vs PRO comparison |

## Webhook Events

| Event | Action |
|-------|--------|
| `checkout.session.completed` | Set plan=pro, store subscription ID and period end |
| `customer.subscription.updated` | Update period end; if `cancel_at_period_end`, set downgrade pending |
| `customer.subscription.deleted` | If repos <= 5: downgrade to free. If repos > 5: set `downgrade_pending` |
| `invoice.payment_failed` | Log warning (Stripe handles retries automatically) |

## Downgrade Policy

- User must manually reduce repos to 5 or fewer before downgrade completes
- Alternatively, user can bulk-delete all repos
- No automatic deactivation of repos
- While `downgrade_pending` is true: dashboard shows warning banner, new repo adds are blocked by existing limit check, imports continue until `plan_period_end`

## Database Changes

Migration: `pkg/data/postgres/sql/migrations_saas/002_stripe_billing.sql`

New columns on `tenant`:

| Column | Type | Purpose |
|--------|------|---------|
| `stripe_customer_id` | `TEXT` | Stripe Customer ID (`cus_xxx`), created on first checkout |
| `stripe_subscription_id` | `TEXT` | Active subscription (`sub_xxx`), NULL when free |
| `plan_period_end` | `TIMESTAMPTZ` | Current billing period end, used for downgrade grace |
| `downgrade_pending` | `BOOLEAN DEFAULT FALSE` | True when subscription cancelled but repos exceed free limit |

## Package Layout

```
pkg/billing/
  billing.go       Config, LoadConfig, CreateCustomer, CreateCheckoutSession, CreatePortalSession
  webhook.go       WebhookHandler, event handlers for checkout/subscription/invoice
  billing_test.go  Unit tests for config and price resolution
  webhook_test.go  Webhook signature verification test

pkg/server/
  billing.go       checkoutHandler, billingPortalHandler
  server.go        Route wiring (gated by stripeCfg != nil)

pkg/tenant/
  tenant.go        Stripe fields on Tenant struct, UpdateStripeCustomer, UpdateSubscription,
                   ClearSubscription, SetDowngradePending, DowngradeToFree, GetTenantByStripeCustomer
  overview.go      UsageSummary includes downgrade_pending and has_billing

infra/saas/
  variables.tf     stripe_pro_monthly_price_id, stripe_pro_annual_price_id
  secrets.tf       stripe-secret-key, stripe-webhook-secret in Secret Manager
  cloudrun.tf      Env vars on devpulse-site Cloud Run service
```

## Pre-Merge Action Items

Complete these before merging `feat/stripe-integration` into `main`:

### 1. Stripe Account Setup

- [ ] Create Stripe account at https://dashboard.stripe.com
- [ ] Stay in **test mode** for initial validation

### 2. Create Product and Prices

- [ ] Create product "DevPulse PRO" in Stripe Dashboard
- [ ] Create monthly price: $19/month recurring
- [ ] Create annual price: $190/year recurring
- [ ] Note the Price IDs (`price_xxx`) for both

### 3. Configure Customer Portal

- [ ] Go to Stripe Dashboard > Settings > Customer Portal
- [ ] Enable: cancel subscription, switch between monthly/annual
- [ ] Enable: update payment method
- [ ] Set return URL to `https://devpulse.thingz.io/dashboard`

### 4. Set Up Webhook Endpoint

- [ ] Go to Stripe Dashboard > Developers > Webhooks
- [ ] Add endpoint: `https://devpulse.thingz.io/webhooks/stripe`
- [ ] Select events: `checkout.session.completed`, `customer.subscription.updated`, `customer.subscription.deleted`, `invoice.payment_failed`
- [ ] Note the webhook signing secret (`whsec_xxx`)

### 5. Store Secrets

- [ ] Store `STRIPE_SECRET_KEY` in GCP Secret Manager as `stripe-secret-key`
- [ ] Store `STRIPE_WEBHOOK_SECRET` in GCP Secret Manager as `stripe-webhook-secret`

### 6. Set Terraform Variables

- [ ] Set `stripe_pro_monthly_price_id` to the monthly Price ID
- [ ] Set `stripe_pro_annual_price_id` to the annual Price ID

### 7. Deploy

- [ ] Run `terraform apply` to create Secret Manager resources and update Cloud Run env vars
- [ ] Deploy new image with Stripe code (`make bump-patch` or manual tag)
- [ ] Verify `/pricing` page loads
- [ ] Test checkout flow with Stripe test card (`4242 4242 4242 4242`)
- [ ] Verify webhook fires and tenant is upgraded
- [ ] Test Customer Portal access from dashboard
- [ ] Test cancellation flow and downgrade behavior

### 8. Go Live

- [ ] Switch Stripe to **live mode**
- [ ] Create live product + prices (same amounts)
- [ ] Update secrets and Price ID env vars with live keys
- [ ] Re-deploy
- [ ] Monitor webhook delivery in Stripe Dashboard

## Local Development

For local testing, use Stripe test mode keys:

```bash
export STRIPE_SECRET_KEY=sk_test_xxx
export STRIPE_WEBHOOK_SECRET=whsec_xxx
export STRIPE_PRO_MONTHLY_PRICE_ID=price_xxx
export STRIPE_PRO_ANNUAL_PRICE_ID=price_xxx
```

Use [Stripe CLI](https://stripe.com/docs/stripe-cli) to forward webhooks locally:

```bash
stripe listen --forward-to localhost:8080/webhooks/stripe
```

Test cards: `4242 4242 4242 4242` (success), `4000 0000 0000 0002` (decline).

## Out of Scope (Future)

- Enterprise self-service pricing
- Tax collection (Stripe Tax — 0.5% per transaction)
- Promo codes / coupons (configurable in Stripe Dashboard without code changes)
- Custom invoicing / receipts (Stripe emails these automatically)
- `upgrade_requested_at` column cleanup (remove after full rollout)
