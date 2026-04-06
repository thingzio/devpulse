-- Force RLS on all tables so policies are enforced even when connected as the table owner.
-- Without this, PostgreSQL bypasses RLS for the table owner, making tenant isolation ineffective.
--
-- Bypass policies allow full access when app.tenant_id is not set (importer, admin),
-- while the existing tenant_repo_filter/tenant_direct policies restrict access when it is set
-- (site service scoped store). PostgreSQL evaluates policies with OR — if any policy grants
-- access, the row is visible. When app.tenant_id is empty, the bypass policy passes and the
-- tenant filter fails (harmlessly). When app.tenant_id is set, the bypass policy fails and
-- the tenant filter enforces scoping.

-- Force RLS for table owner on data tables
ALTER TABLE event FORCE ROW LEVEL SECURITY;
ALTER TABLE developer FORCE ROW LEVEL SECURITY;
ALTER TABLE repo_meta FORCE ROW LEVEL SECURITY;
ALTER TABLE release FORCE ROW LEVEL SECURITY;
ALTER TABLE release_asset FORCE ROW LEVEL SECURITY;
ALTER TABLE container_version FORCE ROW LEVEL SECURITY;
ALTER TABLE repo_metric_history FORCE ROW LEVEL SECURITY;
ALTER TABLE repo_insights FORCE ROW LEVEL SECURITY;

-- Force RLS for table owner on tenant-scoped tables
ALTER TABLE tenant_repo FORCE ROW LEVEL SECURITY;
ALTER TABLE tenant_member FORCE ROW LEVEL SECURITY;
ALTER TABLE github_app_installation FORCE ROW LEVEL SECURITY;
ALTER TABLE session FORCE ROW LEVEL SECURITY;

-- Bypass policies: allow full access when app.tenant_id is not set.
-- The importer and admin service never call set_config, so current_setting returns ''.
CREATE POLICY bypass_when_no_tenant ON event
    USING (current_setting('app.tenant_id', true) = '');
CREATE POLICY bypass_when_no_tenant ON developer
    USING (current_setting('app.tenant_id', true) = '');
CREATE POLICY bypass_when_no_tenant ON repo_meta
    USING (current_setting('app.tenant_id', true) = '');
CREATE POLICY bypass_when_no_tenant ON release
    USING (current_setting('app.tenant_id', true) = '');
CREATE POLICY bypass_when_no_tenant ON release_asset
    USING (current_setting('app.tenant_id', true) = '');
CREATE POLICY bypass_when_no_tenant ON container_version
    USING (current_setting('app.tenant_id', true) = '');
CREATE POLICY bypass_when_no_tenant ON repo_metric_history
    USING (current_setting('app.tenant_id', true) = '');
CREATE POLICY bypass_when_no_tenant ON repo_insights
    USING (current_setting('app.tenant_id', true) = '');
CREATE POLICY bypass_when_no_tenant ON tenant_repo
    USING (current_setting('app.tenant_id', true) = '');
CREATE POLICY bypass_when_no_tenant ON tenant_member
    USING (current_setting('app.tenant_id', true) = '');
CREATE POLICY bypass_when_no_tenant ON github_app_installation
    USING (current_setting('app.tenant_id', true) = '');
CREATE POLICY bypass_when_no_tenant ON session
    USING (current_setting('app.tenant_id', true) = '');
