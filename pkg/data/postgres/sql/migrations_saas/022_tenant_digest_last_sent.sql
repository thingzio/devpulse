-- 022: Add digest_last_sent_at to track when a tenant last received a digest email.
ALTER TABLE devpulse_tenant
    ADD COLUMN IF NOT EXISTS digest_last_sent_at TIMESTAMPTZ;
