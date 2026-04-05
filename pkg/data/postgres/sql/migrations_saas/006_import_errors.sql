ALTER TABLE tenant_repo
    ADD COLUMN IF NOT EXISTS import_errors    INT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS import_last_error TEXT;
