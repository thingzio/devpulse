-- Add status column to tenant table for account suspension.
-- Values: 'active' (default), 'suspended'.
ALTER TABLE devpulse_tenant ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'active';
