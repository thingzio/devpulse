-- Rename existing tables to devpulse_ prefix (production upgrade).
-- Each block is idempotent: skips if the old table no longer exists.

-- Tables
DO $$ BEGIN
  ALTER TABLE developer RENAME TO devpulse_developer;
EXCEPTION WHEN undefined_table THEN NULL;
END $$;

DO $$ BEGIN
  ALTER TABLE event RENAME TO devpulse_event;
EXCEPTION WHEN undefined_table THEN NULL;
END $$;

DO $$ BEGIN
  ALTER TABLE repo_meta RENAME TO devpulse_repo_meta;
EXCEPTION WHEN undefined_table THEN NULL;
END $$;

DO $$ BEGIN
  ALTER TABLE release RENAME TO devpulse_release;
EXCEPTION WHEN undefined_table THEN NULL;
END $$;

DO $$ BEGIN
  ALTER TABLE release_asset RENAME TO devpulse_release_asset;
EXCEPTION WHEN undefined_table THEN NULL;
END $$;

DO $$ BEGIN
  ALTER TABLE container_version RENAME TO devpulse_container_version;
EXCEPTION WHEN undefined_table THEN NULL;
END $$;

DO $$ BEGIN
  ALTER TABLE repo_metric_history RENAME TO devpulse_repo_metric_history;
EXCEPTION WHEN undefined_table THEN NULL;
END $$;

DO $$ BEGIN
  ALTER TABLE repo_insights RENAME TO devpulse_repo_insights;
EXCEPTION WHEN undefined_table THEN NULL;
END $$;

DO $$ BEGIN
  ALTER TABLE state RENAME TO devpulse_state;
EXCEPTION WHEN undefined_table THEN NULL;
END $$;

DO $$ BEGIN
  ALTER TABLE sub RENAME TO devpulse_sub;
EXCEPTION WHEN undefined_table THEN NULL;
END $$;

-- Indexes (from 001)
DO $$ BEGIN
  ALTER INDEX idx_event_org_repo_date RENAME TO idx_devpulse_event_org_repo_date;
EXCEPTION WHEN undefined_table THEN NULL; WHEN undefined_object THEN NULL;
END $$;

DO $$ BEGIN
  ALTER INDEX idx_event_org_repo_type_date RENAME TO idx_devpulse_event_org_repo_type_date;
EXCEPTION WHEN undefined_table THEN NULL; WHEN undefined_object THEN NULL;
END $$;

DO $$ BEGIN
  ALTER INDEX idx_event_org_repo_created_at RENAME TO idx_devpulse_event_org_repo_created_at;
EXCEPTION WHEN undefined_table THEN NULL; WHEN undefined_object THEN NULL;
END $$;

DO $$ BEGIN
  ALTER INDEX idx_event_username RENAME TO idx_devpulse_event_username;
EXCEPTION WHEN undefined_table THEN NULL; WHEN undefined_object THEN NULL;
END $$;

DO $$ BEGIN
  ALTER INDEX idx_developer_reputation RENAME TO idx_devpulse_developer_reputation;
EXCEPTION WHEN undefined_table THEN NULL; WHEN undefined_object THEN NULL;
END $$;

-- Indexes (from 003)
DO $$ BEGIN
  ALTER INDEX idx_event_org_repo_number RENAME TO idx_devpulse_event_org_repo_number;
EXCEPTION WHEN undefined_table THEN NULL; WHEN undefined_object THEN NULL;
END $$;

DO $$ BEGIN
  ALTER INDEX idx_event_username_org_repo RENAME TO idx_devpulse_event_username_org_repo;
EXCEPTION WHEN undefined_table THEN NULL; WHEN undefined_object THEN NULL;
END $$;

DO $$ BEGIN
  ALTER INDEX idx_event_org_repo_number_type RENAME TO idx_devpulse_event_org_repo_number_type;
EXCEPTION WHEN undefined_table THEN NULL; WHEN undefined_object THEN NULL;
END $$;

DO $$ BEGIN
  ALTER INDEX idx_developer_entity_null RENAME TO idx_devpulse_developer_entity_null;
EXCEPTION WHEN undefined_table THEN NULL; WHEN undefined_object THEN NULL;
END $$;
