-- Progressive backfill: track how far back each event type has been backfilled.
ALTER TABLE devpulse_state ADD COLUMN IF NOT EXISTS backfill_until INTEGER;
