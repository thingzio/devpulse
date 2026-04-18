-- 018: Drop legacy import claim queue columns and index.
-- These were retained for rollback safety after the sharded import refactor (v0.35.0).
-- The import pipeline no longer uses per-row claim/done tracking.

DROP INDEX IF EXISTS idx_devpulse_tenant_repo_import_queue;

ALTER TABLE devpulse_tenant_repo
    DROP COLUMN IF EXISTS import_claimed_at,
    DROP COLUMN IF EXISTS import_claimed_by,
    DROP COLUMN IF EXISTS import_done_at;
