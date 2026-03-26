-- Tenant tables for multi-tenant SaaS

CREATE TABLE IF NOT EXISTS tenant (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    github_id       BIGINT UNIQUE NOT NULL,
    username        TEXT NOT NULL,
    email           TEXT,
    avatar_url      TEXT,
    max_repos       INT NOT NULL DEFAULT 3,
    plan            TEXT NOT NULL DEFAULT 'free',
    tos_accepted_at TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
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

CREATE INDEX idx_session_tenant ON session(tenant_id);
CREATE INDEX idx_session_expires ON session(expires_at);
CREATE INDEX idx_tenant_repo_tenant ON tenant_repo(tenant_id, active);
CREATE INDEX idx_tenant_member_github ON tenant_member(github_id);
CREATE INDEX idx_github_app_installation_tenant ON github_app_installation(tenant_id);
