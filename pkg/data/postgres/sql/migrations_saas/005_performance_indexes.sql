-- Covering index for RLS policy EXISTS checks on tenant_repo
-- Avoids heap fetch to verify active flag on every RLS-scoped query
CREATE INDEX IF NOT EXISTS idx_tenant_repo_rls ON tenant_repo (tenant_id, org, repo) WHERE active = TRUE;
