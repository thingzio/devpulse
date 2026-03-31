ALTER TABLE tenant_repo
    ADD COLUMN IF NOT EXISTS import_claimed_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS import_claimed_by TEXT,
    ADD COLUMN IF NOT EXISTS import_done_at    TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS idx_tenant_repo_import_queue
    ON tenant_repo (active, import_claimed_at, import_done_at)
    WHERE active = TRUE;
