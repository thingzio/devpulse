-- Add import tracking columns to devpulse_tenant_repo.
-- These were added to 001_initial.sql after the initial migration had already
-- run on existing databases. This migration adds them idempotently.

ALTER TABLE devpulse_tenant_repo ADD COLUMN IF NOT EXISTS import_claimed_at TIMESTAMPTZ;
ALTER TABLE devpulse_tenant_repo ADD COLUMN IF NOT EXISTS import_claimed_by TEXT;
ALTER TABLE devpulse_tenant_repo ADD COLUMN IF NOT EXISTS import_done_at   TIMESTAMPTZ;
ALTER TABLE devpulse_tenant_repo ADD COLUMN IF NOT EXISTS import_errors    INT NOT NULL DEFAULT 0;
ALTER TABLE devpulse_tenant_repo ADD COLUMN IF NOT EXISTS import_last_error TEXT;
