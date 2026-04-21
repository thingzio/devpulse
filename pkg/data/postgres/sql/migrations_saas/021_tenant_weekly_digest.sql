-- Add weekly digest opt-out column to tenant table.
-- Defaults to TRUE so existing tenants are opted-in.

ALTER TABLE devpulse_tenant
    ADD COLUMN IF NOT EXISTS weekly_digest BOOLEAN NOT NULL DEFAULT TRUE;
