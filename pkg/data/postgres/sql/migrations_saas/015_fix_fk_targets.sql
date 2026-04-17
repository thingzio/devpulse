-- Fix FK constraints on devpulse_session and devpulse_github_app_installation
-- that incorrectly reference devtrace_tenant instead of devpulse_tenant.
--
-- Root cause: both apps originally shared unprefixed table names (tenant,
-- session, github_app_installation). When devtrace renamed tenant →
-- devtrace_tenant, PostgreSQL's FK OIDs followed the rename. DevPulse then
-- created devpulse_tenant as a new table, but the old FK constraints still
-- pointed to the OID that became devtrace_tenant.

-- devpulse_session: drop any existing FK on tenant_id, recreate correctly.
ALTER TABLE devpulse_session DROP CONSTRAINT IF EXISTS session_tenant_id_fkey;
ALTER TABLE devpulse_session DROP CONSTRAINT IF EXISTS devpulse_session_tenant_id_fkey;
ALTER TABLE devpulse_session
    ADD CONSTRAINT devpulse_session_tenant_id_fkey
    FOREIGN KEY (tenant_id) REFERENCES devpulse_tenant(id) ON DELETE CASCADE;

-- devpulse_github_app_installation: drop any existing FK on tenant_id, recreate correctly.
ALTER TABLE devpulse_github_app_installation DROP CONSTRAINT IF EXISTS github_app_installation_tenant_id_fkey;
ALTER TABLE devpulse_github_app_installation DROP CONSTRAINT IF EXISTS devpulse_github_app_installation_tenant_id_fkey;
ALTER TABLE devpulse_github_app_installation
    ADD CONSTRAINT devpulse_github_app_installation_tenant_id_fkey
    FOREIGN KEY (tenant_id) REFERENCES devpulse_tenant(id) ON DELETE CASCADE;
