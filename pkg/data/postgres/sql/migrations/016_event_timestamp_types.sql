-- devpulse_event stored its date/time columns as TEXT since the original
-- schema. Move them to native DATE/TIMESTAMPTZ so the analytics tables match
-- the convention used by every other table in the shared instance (devtrace
-- and devradar are 100% native across 116 temporal columns; devpulse's own
-- saas-chain tables are native too).
--
-- A full audit of the 262,495 production rows found every value already in
-- canonical form ('YYYY-MM-DD' for date, RFC3339 with second precision for
-- the timestamps) with no empty strings and no sub-second components, so the
-- cast is lossless. NULLIF is retained as cheap insurance against a legacy
-- empty string that predates the audit.
--
-- The type guard makes this a no-op on databases created from the current
-- 001_initial.sql, which already declares the native types. Without it the
-- '' literal inside NULLIF would be coerced to DATE and fail at parse time.
--
-- On an existing database this rewrites the table under ACCESS EXCLUSIVE and
-- rebuilds its indexes; measured at ~2.8s for this row count and index set.

DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'devpulse_event'
          AND table_schema = current_schema()
          AND column_name = 'date'
          AND data_type = 'text'
    ) THEN
        ALTER TABLE devpulse_event
            ALTER COLUMN date TYPE DATE USING NULLIF(date, '')::date,
            ALTER COLUMN created_at TYPE TIMESTAMPTZ USING NULLIF(created_at, '')::timestamptz,
            ALTER COLUMN closed_at  TYPE TIMESTAMPTZ USING NULLIF(closed_at, '')::timestamptz,
            ALTER COLUMN merged_at  TYPE TIMESTAMPTZ USING NULLIF(merged_at, '')::timestamptz;
    END IF;
END $$;
