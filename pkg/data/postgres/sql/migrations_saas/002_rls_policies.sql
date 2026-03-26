-- RLS policies for tenant data isolation
-- Global tables: scoped via tenant_repo join
-- Tenant-scoped tables: direct tenant_id match

-- Enable RLS on global tables
ALTER TABLE event ENABLE ROW LEVEL SECURITY;
ALTER TABLE developer ENABLE ROW LEVEL SECURITY;
ALTER TABLE repo_meta ENABLE ROW LEVEL SECURITY;
ALTER TABLE release ENABLE ROW LEVEL SECURITY;
ALTER TABLE release_asset ENABLE ROW LEVEL SECURITY;
ALTER TABLE container_package ENABLE ROW LEVEL SECURITY;
ALTER TABLE repo_metric_history ENABLE ROW LEVEL SECURITY;
ALTER TABLE community_health ENABLE ROW LEVEL SECURITY;
ALTER TABLE reputation ENABLE ROW LEVEL SECURITY;

-- Global table policies: tenant sees data only for their tracked repos
CREATE POLICY tenant_repo_filter ON event
    USING (EXISTS (
        SELECT 1 FROM tenant_repo tr
        WHERE tr.tenant_id = current_setting('app.tenant_id', true)::uuid
          AND tr.org = event.org AND tr.repo = event.repo AND tr.active = true
    ));

CREATE POLICY tenant_repo_filter ON repo_meta
    USING (EXISTS (
        SELECT 1 FROM tenant_repo tr
        WHERE tr.tenant_id = current_setting('app.tenant_id', true)::uuid
          AND tr.org = repo_meta.org AND tr.repo = repo_meta.repo AND tr.active = true
    ));

CREATE POLICY tenant_repo_filter ON release
    USING (EXISTS (
        SELECT 1 FROM tenant_repo tr
        WHERE tr.tenant_id = current_setting('app.tenant_id', true)::uuid
          AND tr.org = release.org AND tr.repo = release.repo AND tr.active = true
    ));

CREATE POLICY tenant_repo_filter ON release_asset
    USING (EXISTS (
        SELECT 1 FROM tenant_repo tr
        JOIN release rl ON rl.org = tr.org AND rl.repo = tr.repo
        WHERE tr.tenant_id = current_setting('app.tenant_id', true)::uuid
          AND rl.id = release_asset.release_id AND tr.active = true
    ));

CREATE POLICY tenant_repo_filter ON container_package
    USING (EXISTS (
        SELECT 1 FROM tenant_repo tr
        WHERE tr.tenant_id = current_setting('app.tenant_id', true)::uuid
          AND tr.org = container_package.org AND tr.repo = container_package.repo AND tr.active = true
    ));

CREATE POLICY tenant_repo_filter ON repo_metric_history
    USING (EXISTS (
        SELECT 1 FROM tenant_repo tr
        WHERE tr.tenant_id = current_setting('app.tenant_id', true)::uuid
          AND tr.org = repo_metric_history.org AND tr.repo = repo_metric_history.repo AND tr.active = true
    ));

CREATE POLICY tenant_repo_filter ON community_health
    USING (EXISTS (
        SELECT 1 FROM tenant_repo tr
        WHERE tr.tenant_id = current_setting('app.tenant_id', true)::uuid
          AND tr.org = community_health.org AND tr.repo = community_health.repo AND tr.active = true
    ));

-- Developer: visible if they contributed to any tenant repo
CREATE POLICY tenant_developer_filter ON developer
    USING (EXISTS (
        SELECT 1 FROM event e
        JOIN tenant_repo tr ON tr.org = e.org AND tr.repo = e.repo AND tr.active = true
        WHERE tr.tenant_id = current_setting('app.tenant_id', true)::uuid
          AND e.username = developer.username
    ));

-- Reputation: visible if developer has contributions in tenant's repos
CREATE POLICY tenant_reputation_filter ON reputation
    USING (EXISTS (
        SELECT 1 FROM event e
        JOIN tenant_repo tr ON tr.org = e.org AND tr.repo = e.repo AND tr.active = true
        WHERE tr.tenant_id = current_setting('app.tenant_id', true)::uuid
          AND e.username = reputation.username
    ));

-- Enable RLS on tenant-scoped tables
ALTER TABLE tenant_repo ENABLE ROW LEVEL SECURITY;
ALTER TABLE tenant_member ENABLE ROW LEVEL SECURITY;
ALTER TABLE github_app_installation ENABLE ROW LEVEL SECURITY;
ALTER TABLE session ENABLE ROW LEVEL SECURITY;

CREATE POLICY tenant_direct ON tenant_repo
    USING (tenant_id = current_setting('app.tenant_id', true)::uuid);
CREATE POLICY tenant_direct ON tenant_member
    USING (tenant_id = current_setting('app.tenant_id', true)::uuid);
CREATE POLICY tenant_direct ON github_app_installation
    USING (tenant_id = current_setting('app.tenant_id', true)::uuid);
CREATE POLICY tenant_direct ON session
    USING (tenant_id = current_setting('app.tenant_id', true)::uuid);
