-- Drop unused deep reputation columns from devpulse_developer.
-- The reputation and reputation_updated_at columns are retained (shallow scoring).

ALTER TABLE devpulse_developer DROP COLUMN IF EXISTS reputation_deep;
ALTER TABLE devpulse_developer DROP COLUMN IF EXISTS reputation_signals;
