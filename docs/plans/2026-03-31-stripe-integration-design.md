# Stripe Payment Integration Design

Automate Free-to-PRO upgrades via Stripe Checkout. Enterprise stays sales-led.

## Pricing Model

- Monthly and annual subscription options
- Prices defined in Stripe Dashboard, referenced by Price ID env vars
- Display prices hardcoded in HTML (change in two places when updating)

## Approach: Stripe Checkout + Webhooks

User clicks Subscribe -> backend creates Stripe Checkout Session -> redirect to Stripe-hosted page -> webhook fires -> backend upgrades tenant. Manage subscription via Stripe Customer Portal redirect.

## Data Model Changes

Add columns to `tenant` table:

```sql
stripe_customer_id     TEXT,          -- Stripe Customer ID (cus_xxx)
stripe_subscription_id TEXT,          -- Active subscription ID (sub_xxx)
plan_period_end        TIMESTAMPTZ,  -- Current billing period end
```

Tenant column checklist: update `upsertTenantSQL`, `getTenantByGitHubIDSQL`, `getTenantByIDSQL`, `validateSessionSQL`, `scanTenant()`, `Tenant` struct, and add migration.

`upgrade_requested_at` becomes unused after full rollout (remove in follow-up).

## API Endpoints

| Method | Path | Auth | Purpose |
|--------|------|------|---------|
| `POST` | `/api/checkout` | session | Create Stripe Checkout Session, return redirect URL. Body: `{"period": "monthly"\|"annual"}` |
| `GET` | `/api/billing/portal` | session | Create Stripe Customer Portal session, redirect |
| `POST` | `/webhooks/stripe` | HMAC signature | Receive Stripe events, update tenant plan |
| `GET` | `/pricing` | public | Pricing page (Free vs PRO comparison) |

## Checkout Flow

1. Frontend `POST /api/checkout` with period choice
2. Backend creates Stripe Customer (if `stripe_customer_id` is NULL), then Checkout Session with `customer`, `price_id`, and `metadata: {tenant_id}`
3. Returns `{url: "https://checkout.stripe.com/..."}`
4. Frontend redirects user there
5. Stripe redirects back to `/pricing?status=success` or `/pricing?status=cancel`

## Webhook Events

| Event | Action |
|-------|--------|
| `checkout.session.completed` | Set plan=pro, store subscription ID and period end |
| `customer.subscription.updated` | Update `plan_period_end`, handle plan changes |
| `customer.subscription.deleted` | Downgrade to free if repos <= 5, else set `downgrade_pending` |
| `invoice.payment_failed` | Log warning (Stripe handles retries) |

## Downgrade Policy

- User must manually reduce repos to <= 5 before downgrade completes, OR opt to bulk-delete all repos
- No auto-deactivation of repos
- On `subscription.deleted`: if repos <= 5, downgrade immediately. If repos > 5, show banner: "Remove repos to complete downgrade." Block new repo adds. Imports continue until `plan_period_end`.

## Stripe Customer Portal

Used for: cancel subscription, switch monthly/annual, update payment method. Redirect from dashboard "Manage Billing" link.

## Environment Variables

| Variable | Storage | Purpose |
|----------|---------|---------|
| `STRIPE_SECRET_KEY` | Secret Manager | Server-side API key |
| `STRIPE_PUBLISHABLE_KEY` | TF variable | Frontend checkout (if needed) |
| `STRIPE_WEBHOOK_SECRET` | Secret Manager | Webhook signature verification |
| `STRIPE_PRO_MONTHLY_PRICE_ID` | Env var | Monthly price reference |
| `STRIPE_PRO_ANNUAL_PRICE_ID` | Env var | Annual price reference |

## New Package

`pkg/billing/` — Stripe client wrapper, checkout session creation, webhook handler, customer management. Follows existing patterns: context propagation, structured logging, `*http.Client` with timeout.

## Frontend Changes

- New `/pricing` page template (public)
- Dashboard: "Manage Billing" link for PRO users (redirects to Stripe Portal)
- Existing upgrade request flow replaced with Stripe Checkout redirect
- Inline upgrade prompt on repo limit keeps similar UX but redirects to checkout

## Out of Scope

- Enterprise self-service (stays manual)
- Tax collection (can add Stripe Tax later)
- Invoicing / receipts (Stripe emails these automatically)
- Promo codes / coupons (can add in Stripe Dashboard later without code changes)
