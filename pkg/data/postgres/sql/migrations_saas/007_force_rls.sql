-- Force RLS on all tables so policies are enforced even when connected as the table owner.
-- Without this, PostgreSQL bypasses RLS for the table owner, making tenant isolation ineffective.
--
-- All existing policies are replaced with safe versions that handle the case where
-- app.tenant_id is not set (empty string). The original policies used ::uuid casts that
-- throw exceptions on empty string, which aborts the entire policy evaluation.
-- The new policies use NULLIF to convert '' to NULL, making the cast safe (NULL::uuid = NULL).
--
-- When app.tenant_id is not set (importer, admin): NULLIF returns NULL, all comparisons
-- yield NULL (not true), so the OR'd bypass policy grants access.
-- When app.tenant_id is set (site service): NULLIF passes through the UUID, tenant filter
-- enforces scoping, and the bypass policy returns false.

-- Force RLS for table owner on all RLS-enabled tables.
ALTER TABLE event FORCE ROW LEVEL SECURITY;
ALTER TABLE developer FORCE ROW LEVEL SECURITY;
ALTER TABLE repo_meta FORCE ROW LEVEL SECURITY;
ALTER TABLE release FORCE ROW LEVEL SECURITY;
ALTER TABLE release_asset FORCE ROW LEVEL SECURITY;
ALTER TABLE container_version FORCE ROW LEVEL SECURITY;
ALTER TABLE repo_metric_history FORCE ROW LEVEL SECURITY;
ALTER TABLE repo_insights FORCE ROW LEVEL SECURITY;
ALTER TABLE tenant_repo FORCE ROW LEVEL SECURITY;
ALTER TABLE tenant_member FORCE ROW LEVEL SECURITY;
ALTER TABLE github_app_installation FORCE ROW LEVEL SECURITY;
ALTER TABLE session FORCE ROW LEVEL SECURITY;

-- Replace data table policies with safe UUID cast (NULLIF prevents ''::uuid error).
DROP POLICY IF EXISTS tenant_repo_filter ON event;
CREATE POLICY tenant_repo_filter ON event
    USING (EXISTS (
        SELECT 1 FROM tenant_repo tr
        WHERE tr.tenant_id = NULLIF(COALESCE(current_setting('app.tenant_id', true), ''), '')::uuid
          AND tr.org = event.org AND tr.repo = event.repo AND tr.active = true
    ));

DROP POLICY IF EXISTS tenant_repo_filter ON repo_meta;
CREATE POLICY tenant_repo_filter ON repo_meta
    USING (EXISTS (
        SELECT 1 FROM tenant_repo tr
        WHERE tr.tenant_id = NULLIF(COALESCE(current_setting('app.tenant_id', true), ''), '')::uuid
          AND tr.org = repo_meta.org AND tr.repo = repo_meta.repo AND tr.active = true
    ));

DROP POLICY IF EXISTS tenant_repo_filter ON release;
CREATE POLICY tenant_repo_filter ON release
    USING (EXISTS (
        SELECT 1 FROM tenant_repo tr
        WHERE tr.tenant_id = NULLIF(COALESCE(current_setting('app.tenant_id', true), ''), '')::uuid
          AND tr.org = release.org AND tr.repo = release.repo AND tr.active = true
    ));

DROP POLICY IF EXISTS tenant_repo_filter ON release_asset;
CREATE POLICY tenant_repo_filter ON release_asset
    USING (EXISTS (
        SELECT 1 FROM tenant_repo tr
        WHERE tr.tenant_id = NULLIF(COALESCE(current_setting('app.tenant_id', true), ''), '')::uuid
          AND tr.org = release_asset.org AND tr.repo = release_asset.repo AND tr.active = true
    ));

DROP POLICY IF EXISTS tenant_repo_filter ON container_version;
CREATE POLICY tenant_repo_filter ON container_version
    USING (EXISTS (
        SELECT 1 FROM tenant_repo tr
        WHERE tr.tenant_id = NULLIF(COALESCE(current_setting('app.tenant_id', true), ''), '')::uuid
          AND tr.org = container_version.org AND tr.repo = container_version.repo AND tr.active = true
    ));

DROP POLICY IF EXISTS tenant_repo_filter ON repo_metric_history;
CREATE POLICY tenant_repo_filter ON repo_metric_history
    USING (EXISTS (
        SELECT 1 FROM tenant_repo tr
        WHERE tr.tenant_id = NULLIF(COALESCE(current_setting('app.tenant_id', true), ''), '')::uuid
          AND tr.org = repo_metric_history.org AND tr.repo = repo_metric_history.repo AND tr.active = true
    ));

DROP POLICY IF EXISTS tenant_repo_filter ON repo_insights;
CREATE POLICY tenant_repo_filter ON repo_insights
    USING (EXISTS (
        SELECT 1 FROM tenant_repo tr
        WHERE tr.tenant_id = NULLIF(COALESCE(current_setting('app.tenant_id', true), ''), '')::uuid
          AND tr.org = repo_insights.org AND tr.repo = repo_insights.repo AND tr.active = true
    ));

DROP POLICY IF EXISTS tenant_developer_filter ON developer;
CREATE POLICY tenant_developer_filter ON developer
    USING (EXISTS (
        SELECT 1 FROM event e
        JOIN tenant_repo tr ON tr.org = e.org AND tr.repo = e.repo AND tr.active = true
        WHERE tr.tenant_id = NULLIF(COALESCE(current_setting('app.tenant_id', true), ''), '')::uuid
          AND e.username = developer.username
    ));

-- Replace tenant-scoped table policies with safe UUID cast.
DROP POLICY IF EXISTS tenant_direct ON tenant_repo;
CREATE POLICY tenant_direct ON tenant_repo
    USING (tenant_id = NULLIF(COALESCE(current_setting('app.tenant_id', true), ''), '')::uuid);

DROP POLICY IF EXISTS tenant_direct ON tenant_member;
CREATE POLICY tenant_direct ON tenant_member
    USING (tenant_id = NULLIF(COALESCE(current_setting('app.tenant_id', true), ''), '')::uuid);

DROP POLICY IF EXISTS tenant_direct ON github_app_installation;
CREATE POLICY tenant_direct ON github_app_installation
    USING (tenant_id = NULLIF(COALESCE(current_setting('app.tenant_id', true), ''), '')::uuid);

DROP POLICY IF EXISTS tenant_direct ON session;
CREATE POLICY tenant_direct ON session
    USING (tenant_id = NULLIF(COALESCE(current_setting('app.tenant_id', true), ''), '')::uuid);

-- Bypass policies: allow full access when app.tenant_id is not set.
-- The importer and admin service never call set_config, so current_setting returns ''.
-- Combined with the NULLIF-guarded tenant policies above (which yield NULL = no match),
-- these bypass policies are the only ones that grant access for unscoped connections.
CREATE POLICY bypass_when_no_tenant ON event
    USING (COALESCE(current_setting('app.tenant_id', true), '') = '');
CREATE POLICY bypass_when_no_tenant ON developer
    USING (COALESCE(current_setting('app.tenant_id', true), '') = '');
CREATE POLICY bypass_when_no_tenant ON repo_meta
    USING (COALESCE(current_setting('app.tenant_id', true), '') = '');
CREATE POLICY bypass_when_no_tenant ON release
    USING (COALESCE(current_setting('app.tenant_id', true), '') = '');
CREATE POLICY bypass_when_no_tenant ON release_asset
    USING (COALESCE(current_setting('app.tenant_id', true), '') = '');
CREATE POLICY bypass_when_no_tenant ON container_version
    USING (COALESCE(current_setting('app.tenant_id', true), '') = '');
CREATE POLICY bypass_when_no_tenant ON repo_metric_history
    USING (COALESCE(current_setting('app.tenant_id', true), '') = '');
CREATE POLICY bypass_when_no_tenant ON repo_insights
    USING (COALESCE(current_setting('app.tenant_id', true), '') = '');
CREATE POLICY bypass_when_no_tenant ON tenant_repo
    USING (COALESCE(current_setting('app.tenant_id', true), '') = '');
CREATE POLICY bypass_when_no_tenant ON tenant_member
    USING (COALESCE(current_setting('app.tenant_id', true), '') = '');
CREATE POLICY bypass_when_no_tenant ON github_app_installation
    USING (COALESCE(current_setting('app.tenant_id', true), '') = '');
CREATE POLICY bypass_when_no_tenant ON session
    USING (COALESCE(current_setting('app.tenant_id', true), '') = '');
