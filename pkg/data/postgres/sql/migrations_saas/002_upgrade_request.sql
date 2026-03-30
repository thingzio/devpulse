-- Add upgrade request tracking to tenant table
ALTER TABLE tenant ADD COLUMN IF NOT EXISTS upgrade_requested_at TIMESTAMPTZ;
