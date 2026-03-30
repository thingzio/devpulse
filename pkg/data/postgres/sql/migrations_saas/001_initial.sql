-- DevPulse SaaS schema: tenant tables + RLS policies

-- Tenant tables

CREATE TABLE IF NOT EXISTS tenant (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    github_id       BIGINT UNIQUE NOT NULL,
    username        TEXT NOT NULL,
    email           TEXT,
    avatar_url      TEXT,
    max_repos            INT NOT NULL DEFAULT 5,
    max_events_per_week  INT NOT NULL DEFAULT 2000,
    plan                 TEXT NOT NULL DEFAULT 'free',
    tos_accepted_at      TIMESTAMPTZ,
    upgrade_requested_at TIMESTAMPTZ,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS tenant_member (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   UUID NOT NULL REFERENCES tenant(id) ON DELETE CASCADE,
    github_id   BIGINT NOT NULL,
    username    TEXT NOT NULL,
    role        TEXT NOT NULL DEFAULT 'viewer',
    invited_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    accepted_at TIMESTAMPTZ,
    UNIQUE(tenant_id, github_id)
);

CREATE TABLE IF NOT EXISTS github_app_installation (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id           UUID NOT NULL REFERENCES tenant(id) ON DELETE CASCADE,
    installation_id     BIGINT UNIQUE NOT NULL,
    target_type         TEXT NOT NULL,
    target_login        TEXT NOT NULL,
    permissions         JSONB,
    suspended_at        TIMESTAMPTZ,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS tenant_repo (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   UUID NOT NULL REFERENCES tenant(id) ON DELETE CASCADE,
    org         TEXT NOT NULL,
    repo        TEXT NOT NULL,
    reputation  JSONB,
    insight     JSONB,
    active      BOOLEAN NOT NULL DEFAULT TRUE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(tenant_id, org, repo)
);

CREATE TABLE IF NOT EXISTS session (
    id          TEXT PRIMARY KEY,
    tenant_id   UUID NOT NULL REFERENCES tenant(id) ON DELETE CASCADE,
    expires_at  TIMESTAMPTZ NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_session_tenant ON session(tenant_id);
CREATE INDEX IF NOT EXISTS idx_session_expires ON session(expires_at);
CREATE INDEX IF NOT EXISTS idx_tenant_repo_tenant ON tenant_repo(tenant_id, active);
CREATE INDEX IF NOT EXISTS idx_tenant_member_github ON tenant_member(github_id);
CREATE INDEX IF NOT EXISTS idx_github_app_installation_tenant ON github_app_installation(tenant_id);

-- RLS policies: global tables scoped via tenant_repo join

ALTER TABLE event ENABLE ROW LEVEL SECURITY;
ALTER TABLE developer ENABLE ROW LEVEL SECURITY;
ALTER TABLE repo_meta ENABLE ROW LEVEL SECURITY;
ALTER TABLE release ENABLE ROW LEVEL SECURITY;
ALTER TABLE release_asset ENABLE ROW LEVEL SECURITY;
ALTER TABLE container_version ENABLE ROW LEVEL SECURITY;
ALTER TABLE repo_metric_history ENABLE ROW LEVEL SECURITY;
ALTER TABLE repo_insights ENABLE ROW LEVEL SECURITY;

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
        WHERE tr.tenant_id = current_setting('app.tenant_id', true)::uuid
          AND tr.org = release_asset.org AND tr.repo = release_asset.repo AND tr.active = true
    ));

CREATE POLICY tenant_repo_filter ON container_version
    USING (EXISTS (
        SELECT 1 FROM tenant_repo tr
        WHERE tr.tenant_id = current_setting('app.tenant_id', true)::uuid
          AND tr.org = container_version.org AND tr.repo = container_version.repo AND tr.active = true
    ));

CREATE POLICY tenant_repo_filter ON repo_metric_history
    USING (EXISTS (
        SELECT 1 FROM tenant_repo tr
        WHERE tr.tenant_id = current_setting('app.tenant_id', true)::uuid
          AND tr.org = repo_metric_history.org AND tr.repo = repo_metric_history.repo AND tr.active = true
    ));

CREATE POLICY tenant_repo_filter ON repo_insights
    USING (EXISTS (
        SELECT 1 FROM tenant_repo tr
        WHERE tr.tenant_id = current_setting('app.tenant_id', true)::uuid
          AND tr.org = repo_insights.org AND tr.repo = repo_insights.repo AND tr.active = true
    ));

CREATE POLICY tenant_developer_filter ON developer
    USING (EXISTS (
        SELECT 1 FROM event e
        JOIN tenant_repo tr ON tr.org = e.org AND tr.repo = e.repo AND tr.active = true
        WHERE tr.tenant_id = current_setting('app.tenant_id', true)::uuid
          AND e.username = developer.username
    ));

-- RLS policies: tenant-scoped tables with direct tenant_id

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
