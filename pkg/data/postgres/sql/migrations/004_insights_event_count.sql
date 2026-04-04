-- Add event_count column for insight caching staleness check.
-- Tracks the event count at the time insights were last generated.

ALTER TABLE repo_insights ADD COLUMN IF NOT EXISTS event_count INTEGER NOT NULL DEFAULT 0;
