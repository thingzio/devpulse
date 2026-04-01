-- Add Stripe billing columns to tenant table
ALTER TABLE tenant ADD COLUMN IF NOT EXISTS stripe_customer_id TEXT;
ALTER TABLE tenant ADD COLUMN IF NOT EXISTS stripe_subscription_id TEXT;
ALTER TABLE tenant ADD COLUMN IF NOT EXISTS plan_period_end TIMESTAMPTZ;
ALTER TABLE tenant ADD COLUMN IF NOT EXISTS downgrade_pending BOOLEAN NOT NULL DEFAULT FALSE;

CREATE INDEX IF NOT EXISTS idx_tenant_stripe_customer ON tenant(stripe_customer_id) WHERE stripe_customer_id IS NOT NULL;
