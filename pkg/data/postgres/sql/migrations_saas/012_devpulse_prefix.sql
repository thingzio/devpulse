-- Production migration: rename SaaS tables, indexes, and RLS policies to devpulse_ prefix.
-- Runs against databases at saas migration version 11.
-- Uses DO blocks with EXCEPTION handling so each rename is idempotent.

-- ─── Rename 6 SaaS tables ───────────────────────────────────────────

DO $$ BEGIN ALTER TABLE tenant RENAME TO devpulse_tenant;
EXCEPTION WHEN undefined_table THEN NULL; WHEN duplicate_table THEN NULL; END $$;

DO $$ BEGIN ALTER TABLE tenant_member RENAME TO devpulse_tenant_member;
EXCEPTION WHEN undefined_table THEN NULL; WHEN duplicate_table THEN NULL; END $$;

DO $$ BEGIN ALTER TABLE github_app_installation RENAME TO devpulse_github_app_installation;
EXCEPTION WHEN undefined_table THEN NULL; WHEN duplicate_table THEN NULL; END $$;

DO $$ BEGIN ALTER TABLE tenant_repo RENAME TO devpulse_tenant_repo;
EXCEPTION WHEN undefined_table THEN NULL; WHEN duplicate_table THEN NULL; END $$;

DO $$ BEGIN ALTER TABLE session RENAME TO devpulse_session;
EXCEPTION WHEN undefined_table THEN NULL; WHEN duplicate_table THEN NULL; END $$;

DO $$ BEGIN ALTER TABLE platform_stats RENAME TO devpulse_platform_stats;
EXCEPTION WHEN undefined_table THEN NULL; WHEN duplicate_table THEN NULL; END $$;

-- ─── Rename 10 base data tables ─────────────────────────────────────

DO $$ BEGIN ALTER TABLE event RENAME TO devpulse_event;
EXCEPTION WHEN undefined_table THEN NULL; WHEN duplicate_table THEN NULL; END $$;

DO $$ BEGIN ALTER TABLE developer RENAME TO devpulse_developer;
EXCEPTION WHEN undefined_table THEN NULL; WHEN duplicate_table THEN NULL; END $$;

DO $$ BEGIN ALTER TABLE repo_meta RENAME TO devpulse_repo_meta;
EXCEPTION WHEN undefined_table THEN NULL; WHEN duplicate_table THEN NULL; END $$;

DO $$ BEGIN ALTER TABLE release RENAME TO devpulse_release;
EXCEPTION WHEN undefined_table THEN NULL; WHEN duplicate_table THEN NULL; END $$;

DO $$ BEGIN ALTER TABLE release_asset RENAME TO devpulse_release_asset;
EXCEPTION WHEN undefined_table THEN NULL; WHEN duplicate_table THEN NULL; END $$;

DO $$ BEGIN ALTER TABLE container_version RENAME TO devpulse_container_version;
EXCEPTION WHEN undefined_table THEN NULL; WHEN duplicate_table THEN NULL; END $$;

DO $$ BEGIN ALTER TABLE repo_metric_history RENAME TO devpulse_repo_metric_history;
EXCEPTION WHEN undefined_table THEN NULL; WHEN duplicate_table THEN NULL; END $$;

DO $$ BEGIN ALTER TABLE repo_insights RENAME TO devpulse_repo_insights;
EXCEPTION WHEN undefined_table THEN NULL; WHEN duplicate_table THEN NULL; END $$;

DO $$ BEGIN ALTER TABLE state RENAME TO devpulse_state;
EXCEPTION WHEN undefined_table THEN NULL; WHEN duplicate_table THEN NULL; END $$;

DO $$ BEGIN ALTER TABLE sub RENAME TO devpulse_sub;
EXCEPTION WHEN undefined_table THEN NULL; WHEN duplicate_table THEN NULL; END $$;

-- ─── Rename 7 SaaS indexes ──────────────────────────────────────────

DO $$ BEGIN ALTER INDEX idx_session_tenant RENAME TO idx_devpulse_session_tenant;
EXCEPTION WHEN undefined_table THEN NULL; WHEN undefined_object THEN NULL; END $$;

DO $$ BEGIN ALTER INDEX idx_session_expires RENAME TO idx_devpulse_session_expires;
EXCEPTION WHEN undefined_table THEN NULL; WHEN undefined_object THEN NULL; END $$;

DO $$ BEGIN ALTER INDEX idx_tenant_repo_tenant RENAME TO idx_devpulse_tenant_repo_tenant;
EXCEPTION WHEN undefined_table THEN NULL; WHEN undefined_object THEN NULL; END $$;

DO $$ BEGIN ALTER INDEX idx_tenant_member_github RENAME TO idx_devpulse_tenant_member_github;
EXCEPTION WHEN undefined_table THEN NULL; WHEN undefined_object THEN NULL; END $$;

DO $$ BEGIN ALTER INDEX idx_github_app_installation_tenant RENAME TO idx_devpulse_github_app_installation_tenant;
EXCEPTION WHEN undefined_table THEN NULL; WHEN undefined_object THEN NULL; END $$;

DO $$ BEGIN ALTER INDEX idx_tenant_repo_import_queue RENAME TO idx_devpulse_tenant_repo_import_queue;
EXCEPTION WHEN undefined_table THEN NULL; WHEN undefined_object THEN NULL; END $$;

DO $$ BEGIN ALTER INDEX idx_tenant_repo_rls RENAME TO idx_devpulse_tenant_repo_rls;
EXCEPTION WHEN undefined_table THEN NULL; WHEN undefined_object THEN NULL; END $$;

-- ─── Drop all old RLS policies ──────────────────────────────────────
-- Policy USING clauses contain hardcoded table references (e.g. tenant_repo)
-- that are now invalid after table renames. Must drop and recreate.

-- Old tenant_repo_filter policies on data tables (now renamed to devpulse_*)
DROP POLICY IF EXISTS tenant_repo_filter ON devpulse_event;
DROP POLICY IF EXISTS tenant_repo_filter ON devpulse_repo_meta;
DROP POLICY IF EXISTS tenant_repo_filter ON devpulse_release;
DROP POLICY IF EXISTS tenant_repo_filter ON devpulse_release_asset;
DROP POLICY IF EXISTS tenant_repo_filter ON devpulse_container_version;
DROP POLICY IF EXISTS tenant_repo_filter ON devpulse_repo_metric_history;
DROP POLICY IF EXISTS tenant_repo_filter ON devpulse_repo_insights;

-- Old tenant_developer_filter policy
DROP POLICY IF EXISTS tenant_developer_filter ON devpulse_developer;

-- Old tenant_direct policies on saas tables (now renamed to devpulse_*)
DROP POLICY IF EXISTS tenant_direct ON devpulse_tenant_repo;
DROP POLICY IF EXISTS tenant_direct ON devpulse_tenant_member;
DROP POLICY IF EXISTS tenant_direct ON devpulse_github_app_installation;
DROP POLICY IF EXISTS tenant_direct ON devpulse_session;

-- Old bypass_when_no_tenant policies on all 12 RLS tables
DROP POLICY IF EXISTS bypass_when_no_tenant ON devpulse_event;
DROP POLICY IF EXISTS bypass_when_no_tenant ON devpulse_developer;
DROP POLICY IF EXISTS bypass_when_no_tenant ON devpulse_repo_meta;
DROP POLICY IF EXISTS bypass_when_no_tenant ON devpulse_release;
DROP POLICY IF EXISTS bypass_when_no_tenant ON devpulse_release_asset;
DROP POLICY IF EXISTS bypass_when_no_tenant ON devpulse_container_version;
DROP POLICY IF EXISTS bypass_when_no_tenant ON devpulse_repo_metric_history;
DROP POLICY IF EXISTS bypass_when_no_tenant ON devpulse_repo_insights;
DROP POLICY IF EXISTS bypass_when_no_tenant ON devpulse_tenant_repo;
DROP POLICY IF EXISTS bypass_when_no_tenant ON devpulse_tenant_member;
DROP POLICY IF EXISTS bypass_when_no_tenant ON devpulse_github_app_installation;
DROP POLICY IF EXISTS bypass_when_no_tenant ON devpulse_session;

-- ─── FORCE RLS (idempotent — already enabled, ensures FORCE is set) ─

ALTER TABLE devpulse_event FORCE ROW LEVEL SECURITY;
ALTER TABLE devpulse_developer FORCE ROW LEVEL SECURITY;
ALTER TABLE devpulse_repo_meta FORCE ROW LEVEL SECURITY;
ALTER TABLE devpulse_release FORCE ROW LEVEL SECURITY;
ALTER TABLE devpulse_release_asset FORCE ROW LEVEL SECURITY;
ALTER TABLE devpulse_container_version FORCE ROW LEVEL SECURITY;
ALTER TABLE devpulse_repo_metric_history FORCE ROW LEVEL SECURITY;
ALTER TABLE devpulse_repo_insights FORCE ROW LEVEL SECURITY;
ALTER TABLE devpulse_tenant_repo FORCE ROW LEVEL SECURITY;
ALTER TABLE devpulse_tenant_member FORCE ROW LEVEL SECURITY;
ALTER TABLE devpulse_github_app_installation FORCE ROW LEVEL SECURITY;
ALTER TABLE devpulse_session FORCE ROW LEVEL SECURITY;

-- ─── Recreate tenant_repo_filter policies with devpulse_ references ─
-- Drop first for idempotency (fresh DBs already have these from 001)

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

-- ─── Recreate developer filter policy ───────────────────────────────

DROP POLICY IF EXISTS devpulse_tenant_developer_filter ON devpulse_developer;
CREATE POLICY devpulse_tenant_developer_filter ON devpulse_developer
    USING (EXISTS (
        SELECT 1 FROM devpulse_event e
        JOIN devpulse_tenant_repo tr ON tr.org = e.org AND tr.repo = e.repo AND tr.active = true
        WHERE tr.tenant_id = NULLIF(COALESCE(current_setting('app.tenant_id', true), ''), '')::uuid
          AND e.username = devpulse_developer.username
    ));

-- ─── Recreate direct tenant_id filter policies ──────────────────────

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

-- ─── Recreate bypass policies on all 12 RLS tables ──────────────────

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
