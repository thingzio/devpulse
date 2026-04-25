-- DevPulse SaaS schema: tenant tables, indexes, RLS policies.
-- Squashed from migrations 001-023 (2026-04-25).

-- ─── Tenant tables ──────────────────────────────────────────────────

CREATE TABLE IF NOT EXISTS devpulse_tenant (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    github_id            BIGINT UNIQUE NOT NULL,
    username             TEXT NOT NULL,
    email                TEXT,
    avatar_url           TEXT,
    name                 TEXT,
    company              TEXT,
    location             TEXT,
    bio                  TEXT,
    max_repos            INT NOT NULL DEFAULT 3,
    max_events_per_week  INT NOT NULL DEFAULT 1000,
    plan                 TEXT NOT NULL DEFAULT 'free',
    status               TEXT NOT NULL DEFAULT 'active',
    weekly_digest        BOOLEAN NOT NULL DEFAULT TRUE,
    digest_last_sent_at  TIMESTAMPTZ,
    tos_accepted_at      TIMESTAMPTZ,
    upgrade_requested_at TIMESTAMPTZ,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS devpulse_tenant_member (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   UUID NOT NULL REFERENCES devpulse_tenant(id) ON DELETE CASCADE,
    github_id   BIGINT NOT NULL,
    username    TEXT NOT NULL,
    role        TEXT NOT NULL DEFAULT 'viewer',
    invited_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    accepted_at TIMESTAMPTZ,
    UNIQUE(tenant_id, github_id)
);

CREATE TABLE IF NOT EXISTS devpulse_github_app_installation (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id           UUID NOT NULL REFERENCES devpulse_tenant(id) ON DELETE CASCADE,
    installation_id     BIGINT UNIQUE NOT NULL,
    target_type         TEXT NOT NULL,
    target_login        TEXT NOT NULL,
    permissions         JSONB,
    app_id              BIGINT,
    suspended_at        TIMESTAMPTZ,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS devpulse_tenant_repo (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id        UUID NOT NULL REFERENCES devpulse_tenant(id) ON DELETE CASCADE,
    org              TEXT NOT NULL,
    repo             TEXT NOT NULL,
    reputation       JSONB,
    insight          JSONB,
    active           BOOLEAN NOT NULL DEFAULT TRUE,
    sample           BOOLEAN NOT NULL DEFAULT FALSE,
    import_errors    INT NOT NULL DEFAULT 0,
    import_last_error TEXT,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(tenant_id, org, repo)
);

CREATE TABLE IF NOT EXISTS devpulse_session (
    id          TEXT PRIMARY KEY,
    tenant_id   UUID NOT NULL REFERENCES devpulse_tenant(id) ON DELETE CASCADE,
    expires_at  TIMESTAMPTZ NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS devpulse_platform_stats (
    date                DATE PRIMARY KEY,
    tenants             INT NOT NULL DEFAULT 0,
    tenants_free        INT NOT NULL DEFAULT 0,
    tenants_starter     INT NOT NULL DEFAULT 0,
    tenants_pro         INT NOT NULL DEFAULT 0,
    tenants_enterprise  INT NOT NULL DEFAULT 0,
    repos               INT NOT NULL DEFAULT 0,
    events              BIGINT NOT NULL DEFAULT 0,
    contributors        INT NOT NULL DEFAULT 0,
    installations       INT NOT NULL DEFAULT 0,
    repos_with_errors   INT NOT NULL DEFAULT 0,
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS devpulse_token_quota_sample (
    id              BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    sampled_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    installation_id BIGINT NOT NULL,
    login           TEXT NOT NULL,
    quota_limit     INT NOT NULL,
    quota_used      INT NOT NULL
);

-- ─── Indexes ────────────────────────────────────────────────────────

CREATE INDEX IF NOT EXISTS idx_devpulse_session_tenant
    ON devpulse_session(tenant_id);
CREATE INDEX IF NOT EXISTS idx_devpulse_session_expires
    ON devpulse_session(expires_at);
CREATE INDEX IF NOT EXISTS idx_devpulse_tenant_repo_tenant
    ON devpulse_tenant_repo(tenant_id, active);
CREATE INDEX IF NOT EXISTS idx_devpulse_tenant_member_github
    ON devpulse_tenant_member(github_id);
CREATE INDEX IF NOT EXISTS idx_devpulse_github_app_installation_tenant
    ON devpulse_github_app_installation(tenant_id);
CREATE INDEX IF NOT EXISTS idx_devpulse_tenant_repo_rls
    ON devpulse_tenant_repo(tenant_id, org, repo)
    WHERE active = TRUE;
CREATE INDEX IF NOT EXISTS idx_devpulse_token_quota_sample_time
    ON devpulse_token_quota_sample(sampled_at DESC);
CREATE INDEX IF NOT EXISTS idx_devpulse_event_type_created
    ON devpulse_event (type, created_at);
CREATE INDEX IF NOT EXISTS idx_devpulse_event_username_date
    ON devpulse_event (username, date);

-- ─── FK corrections ─────────────────────────────────────────────────
-- Ensure FK constraints point to devpulse_tenant (not a renamed table).

ALTER TABLE devpulse_session DROP CONSTRAINT IF EXISTS session_tenant_id_fkey;
ALTER TABLE devpulse_session DROP CONSTRAINT IF EXISTS devpulse_session_tenant_id_fkey;
ALTER TABLE devpulse_session
    ADD CONSTRAINT devpulse_session_tenant_id_fkey
    FOREIGN KEY (tenant_id) REFERENCES devpulse_tenant(id) ON DELETE CASCADE;

ALTER TABLE devpulse_github_app_installation DROP CONSTRAINT IF EXISTS github_app_installation_tenant_id_fkey;
ALTER TABLE devpulse_github_app_installation DROP CONSTRAINT IF EXISTS devpulse_github_app_installation_tenant_id_fkey;
ALTER TABLE devpulse_github_app_installation
    ADD CONSTRAINT devpulse_github_app_installation_tenant_id_fkey
    FOREIGN KEY (tenant_id) REFERENCES devpulse_tenant(id) ON DELETE CASCADE;

-- ─── Drop legacy columns (idempotent) ──────────────────────────────

ALTER TABLE devpulse_developer DROP COLUMN IF EXISTS reputation_deep;
ALTER TABLE devpulse_developer DROP COLUMN IF EXISTS reputation_signals;

-- ─── Tenant status column (idempotent for existing DBs) ────────────

ALTER TABLE devpulse_tenant ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'active';
ALTER TABLE devpulse_tenant ADD COLUMN IF NOT EXISTS weekly_digest BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE devpulse_tenant ADD COLUMN IF NOT EXISTS digest_last_sent_at TIMESTAMPTZ;

-- ─── Enable + Force RLS on all data tables ──────────────────────────

ALTER TABLE devpulse_event ENABLE ROW LEVEL SECURITY;
ALTER TABLE devpulse_event FORCE ROW LEVEL SECURITY;
ALTER TABLE devpulse_developer ENABLE ROW LEVEL SECURITY;
ALTER TABLE devpulse_developer FORCE ROW LEVEL SECURITY;
ALTER TABLE devpulse_repo_meta ENABLE ROW LEVEL SECURITY;
ALTER TABLE devpulse_repo_meta FORCE ROW LEVEL SECURITY;
ALTER TABLE devpulse_release ENABLE ROW LEVEL SECURITY;
ALTER TABLE devpulse_release FORCE ROW LEVEL SECURITY;
ALTER TABLE devpulse_release_asset ENABLE ROW LEVEL SECURITY;
ALTER TABLE devpulse_release_asset FORCE ROW LEVEL SECURITY;
ALTER TABLE devpulse_container_version ENABLE ROW LEVEL SECURITY;
ALTER TABLE devpulse_container_version FORCE ROW LEVEL SECURITY;
ALTER TABLE devpulse_repo_metric_history ENABLE ROW LEVEL SECURITY;
ALTER TABLE devpulse_repo_metric_history FORCE ROW LEVEL SECURITY;
ALTER TABLE devpulse_repo_insights ENABLE ROW LEVEL SECURITY;
ALTER TABLE devpulse_repo_insights FORCE ROW LEVEL SECURITY;

-- ─── Enable + Force RLS on tenant-scoped tables ─────────────────────

ALTER TABLE devpulse_tenant_repo ENABLE ROW LEVEL SECURITY;
ALTER TABLE devpulse_tenant_repo FORCE ROW LEVEL SECURITY;
ALTER TABLE devpulse_tenant_member ENABLE ROW LEVEL SECURITY;
ALTER TABLE devpulse_tenant_member FORCE ROW LEVEL SECURITY;
ALTER TABLE devpulse_github_app_installation ENABLE ROW LEVEL SECURITY;
ALTER TABLE devpulse_github_app_installation FORCE ROW LEVEL SECURITY;
ALTER TABLE devpulse_session ENABLE ROW LEVEL SECURITY;
ALTER TABLE devpulse_session FORCE ROW LEVEL SECURITY;

-- ─── Tenant-repo filter policies (7 data tables) ────────────────────

DROP POLICY IF EXISTS devpulse_tenant_repo_filter ON devpulse_event;
CREATE POLICY devpulse_tenant_repo_filter ON devpulse_event
    USING (EXISTS (
        SELECT 1 FROM devpulse_tenant_repo tr
        WHERE tr.tenant_id = NULLIF(COALESCE(current_setting('app.tenant_id', true), ''), '')::uuid
          AND tr.org = devpulse_event.org AND tr.repo = devpulse_event.repo AND tr.active = true
    ));

DROP POLICY IF EXISTS devpulse_tenant_repo_filter ON devpulse_repo_meta;
CREATE POLICY devpulse_tenant_repo_filter ON devpulse_repo_meta
    USING (EXISTS (
        SELECT 1 FROM devpulse_tenant_repo tr
        WHERE tr.tenant_id = NULLIF(COALESCE(current_setting('app.tenant_id', true), ''), '')::uuid
          AND tr.org = devpulse_repo_meta.org AND tr.repo = devpulse_repo_meta.repo AND tr.active = true
    ));

DROP POLICY IF EXISTS devpulse_tenant_repo_filter ON devpulse_release;
CREATE POLICY devpulse_tenant_repo_filter ON devpulse_release
    USING (EXISTS (
        SELECT 1 FROM devpulse_tenant_repo tr
        WHERE tr.tenant_id = NULLIF(COALESCE(current_setting('app.tenant_id', true), ''), '')::uuid
          AND tr.org = devpulse_release.org AND tr.repo = devpulse_release.repo AND tr.active = true
    ));

DROP POLICY IF EXISTS devpulse_tenant_repo_filter ON devpulse_release_asset;
CREATE POLICY devpulse_tenant_repo_filter ON devpulse_release_asset
    USING (EXISTS (
        SELECT 1 FROM devpulse_tenant_repo tr
        WHERE tr.tenant_id = NULLIF(COALESCE(current_setting('app.tenant_id', true), ''), '')::uuid
          AND tr.org = devpulse_release_asset.org AND tr.repo = devpulse_release_asset.repo AND tr.active = true
    ));

DROP POLICY IF EXISTS devpulse_tenant_repo_filter ON devpulse_container_version;
CREATE POLICY devpulse_tenant_repo_filter ON devpulse_container_version
    USING (EXISTS (
        SELECT 1 FROM devpulse_tenant_repo tr
        WHERE tr.tenant_id = NULLIF(COALESCE(current_setting('app.tenant_id', true), ''), '')::uuid
          AND tr.org = devpulse_container_version.org AND tr.repo = devpulse_container_version.repo AND tr.active = true
    ));

DROP POLICY IF EXISTS devpulse_tenant_repo_filter ON devpulse_repo_metric_history;
CREATE POLICY devpulse_tenant_repo_filter ON devpulse_repo_metric_history
    USING (EXISTS (
        SELECT 1 FROM devpulse_tenant_repo tr
        WHERE tr.tenant_id = NULLIF(COALESCE(current_setting('app.tenant_id', true), ''), '')::uuid
          AND tr.org = devpulse_repo_metric_history.org AND tr.repo = devpulse_repo_metric_history.repo AND tr.active = true
    ));

DROP POLICY IF EXISTS devpulse_tenant_repo_filter ON devpulse_repo_insights;
CREATE POLICY devpulse_tenant_repo_filter ON devpulse_repo_insights
    USING (EXISTS (
        SELECT 1 FROM devpulse_tenant_repo tr
        WHERE tr.tenant_id = NULLIF(COALESCE(current_setting('app.tenant_id', true), ''), '')::uuid
          AND tr.org = devpulse_repo_insights.org AND tr.repo = devpulse_repo_insights.repo AND tr.active = true
    ));

-- ─── Developer filter policy (via event join) ───────────────────────

DROP POLICY IF EXISTS devpulse_tenant_developer_filter ON devpulse_developer;
CREATE POLICY devpulse_tenant_developer_filter ON devpulse_developer
    USING (EXISTS (
        SELECT 1 FROM devpulse_event e
        JOIN devpulse_tenant_repo tr ON tr.org = e.org AND tr.repo = e.repo AND tr.active = true
        WHERE tr.tenant_id = NULLIF(COALESCE(current_setting('app.tenant_id', true), ''), '')::uuid
          AND e.username = devpulse_developer.username
    ));

-- ─── Direct tenant_id filter policies (4 tables) ────────────────────

DROP POLICY IF EXISTS devpulse_tenant_direct ON devpulse_tenant_repo;
CREATE POLICY devpulse_tenant_direct ON devpulse_tenant_repo
    USING (tenant_id = NULLIF(COALESCE(current_setting('app.tenant_id', true), ''), '')::uuid);
DROP POLICY IF EXISTS devpulse_tenant_direct ON devpulse_tenant_member;
CREATE POLICY devpulse_tenant_direct ON devpulse_tenant_member
    USING (tenant_id = NULLIF(COALESCE(current_setting('app.tenant_id', true), ''), '')::uuid);
DROP POLICY IF EXISTS devpulse_tenant_direct ON devpulse_github_app_installation;
CREATE POLICY devpulse_tenant_direct ON devpulse_github_app_installation
    USING (tenant_id = NULLIF(COALESCE(current_setting('app.tenant_id', true), ''), '')::uuid);
DROP POLICY IF EXISTS devpulse_tenant_direct ON devpulse_session;
CREATE POLICY devpulse_tenant_direct ON devpulse_session
    USING (tenant_id = NULLIF(COALESCE(current_setting('app.tenant_id', true), ''), '')::uuid);

-- ─── Bypass policies (all 12 RLS tables) ────────────────────────────

DROP POLICY IF EXISTS devpulse_bypass_when_no_tenant ON devpulse_event;
CREATE POLICY devpulse_bypass_when_no_tenant ON devpulse_event
    USING (COALESCE(current_setting('app.tenant_id', true), '') = '');
DROP POLICY IF EXISTS devpulse_bypass_when_no_tenant ON devpulse_developer;
CREATE POLICY devpulse_bypass_when_no_tenant ON devpulse_developer
    USING (COALESCE(current_setting('app.tenant_id', true), '') = '');
DROP POLICY IF EXISTS devpulse_bypass_when_no_tenant ON devpulse_repo_meta;
CREATE POLICY devpulse_bypass_when_no_tenant ON devpulse_repo_meta
    USING (COALESCE(current_setting('app.tenant_id', true), '') = '');
DROP POLICY IF EXISTS devpulse_bypass_when_no_tenant ON devpulse_release;
CREATE POLICY devpulse_bypass_when_no_tenant ON devpulse_release
    USING (COALESCE(current_setting('app.tenant_id', true), '') = '');
DROP POLICY IF EXISTS devpulse_bypass_when_no_tenant ON devpulse_release_asset;
CREATE POLICY devpulse_bypass_when_no_tenant ON devpulse_release_asset
    USING (COALESCE(current_setting('app.tenant_id', true), '') = '');
DROP POLICY IF EXISTS devpulse_bypass_when_no_tenant ON devpulse_container_version;
CREATE POLICY devpulse_bypass_when_no_tenant ON devpulse_container_version
    USING (COALESCE(current_setting('app.tenant_id', true), '') = '');
DROP POLICY IF EXISTS devpulse_bypass_when_no_tenant ON devpulse_repo_metric_history;
CREATE POLICY devpulse_bypass_when_no_tenant ON devpulse_repo_metric_history
    USING (COALESCE(current_setting('app.tenant_id', true), '') = '');
DROP POLICY IF EXISTS devpulse_bypass_when_no_tenant ON devpulse_repo_insights;
CREATE POLICY devpulse_bypass_when_no_tenant ON devpulse_repo_insights
    USING (COALESCE(current_setting('app.tenant_id', true), '') = '');
DROP POLICY IF EXISTS devpulse_bypass_when_no_tenant ON devpulse_tenant_repo;
CREATE POLICY devpulse_bypass_when_no_tenant ON devpulse_tenant_repo
    USING (COALESCE(current_setting('app.tenant_id', true), '') = '');
DROP POLICY IF EXISTS devpulse_bypass_when_no_tenant ON devpulse_tenant_member;
CREATE POLICY devpulse_bypass_when_no_tenant ON devpulse_tenant_member
    USING (COALESCE(current_setting('app.tenant_id', true), '') = '');
DROP POLICY IF EXISTS devpulse_bypass_when_no_tenant ON devpulse_github_app_installation;
CREATE POLICY devpulse_bypass_when_no_tenant ON devpulse_github_app_installation
    USING (COALESCE(current_setting('app.tenant_id', true), '') = '');
DROP POLICY IF EXISTS devpulse_bypass_when_no_tenant ON devpulse_session;
CREATE POLICY devpulse_bypass_when_no_tenant ON devpulse_session
    USING (COALESCE(current_setting('app.tenant_id', true), '') = '');
