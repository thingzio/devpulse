-- Completes the TEXT -> native temporal migration started in 003 for the
-- remaining six base-chain tables. Splitting devpulse_event out kept the one
-- expensive rewrite isolated; these tables are small (92K rows for developer,
-- under 5K for the rest, and container_version is empty in production).
--
-- updated_at/last_import_at/pushed_at carry NOT NULL DEFAULT '', which has no
-- TIMESTAMPTZ equivalent, so the constraint and default are dropped first.
-- The production audit found exactly five empty strings in the entire schema,
-- all in repo_meta.pushed_at -- they become NULL, which is what the write path
-- already means by "no value".
--
-- container_version.created_at is NOT NULL today, but the importer stores ''
-- when GitHub returns no timestamp, so it becomes nullable rather than
-- inventing a sentinel date.
--
-- Each block is guarded on the current column type so this is a no-op against
-- a database created from the current 001_initial.sql, which already declares
-- the native types.

DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'devpulse_repo_meta'
          AND column_name = 'updated_at'
          AND data_type = 'text'
    ) THEN
        ALTER TABLE devpulse_repo_meta
            ALTER COLUMN updated_at DROP NOT NULL,
            ALTER COLUMN updated_at DROP DEFAULT,
            ALTER COLUMN updated_at TYPE TIMESTAMPTZ USING NULLIF(updated_at, '')::timestamptz,
            ALTER COLUMN last_import_at DROP NOT NULL,
            ALTER COLUMN last_import_at DROP DEFAULT,
            ALTER COLUMN last_import_at TYPE TIMESTAMPTZ USING NULLIF(last_import_at, '')::timestamptz,
            ALTER COLUMN pushed_at DROP NOT NULL,
            ALTER COLUMN pushed_at DROP DEFAULT,
            ALTER COLUMN pushed_at TYPE TIMESTAMPTZ USING NULLIF(pushed_at, '')::timestamptz;
    END IF;

    IF EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'devpulse_developer'
          AND column_name = 'reputation_updated_at'
          AND data_type = 'text'
    ) THEN
        ALTER TABLE devpulse_developer
            ALTER COLUMN reputation_updated_at TYPE TIMESTAMPTZ
                USING NULLIF(reputation_updated_at, '')::timestamptz;
    END IF;

    IF EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'devpulse_release'
          AND column_name = 'published_at'
          AND data_type = 'text'
    ) THEN
        ALTER TABLE devpulse_release
            ALTER COLUMN published_at TYPE TIMESTAMPTZ
                USING NULLIF(published_at, '')::timestamptz;
    END IF;

    IF EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'devpulse_container_version'
          AND column_name = 'created_at'
          AND data_type = 'text'
    ) THEN
        ALTER TABLE devpulse_container_version
            ALTER COLUMN created_at DROP NOT NULL,
            ALTER COLUMN created_at TYPE TIMESTAMPTZ USING NULLIF(created_at, '')::timestamptz;
    END IF;

    IF EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'devpulse_repo_metric_history'
          AND column_name = 'date'
          AND data_type = 'text'
    ) THEN
        ALTER TABLE devpulse_repo_metric_history
            ALTER COLUMN date TYPE DATE USING NULLIF(date, '')::date;
    END IF;

    IF EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'devpulse_repo_insights'
          AND column_name = 'generated_at'
          AND data_type = 'text'
    ) THEN
        ALTER TABLE devpulse_repo_insights
            ALTER COLUMN generated_at TYPE TIMESTAMPTZ
                USING NULLIF(generated_at, '')::timestamptz;
    END IF;
END $$;
