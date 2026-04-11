-- Add profile columns to tenant table for existing databases.
-- These were added to 001_initial.sql for fresh installs but need
-- an incremental migration for databases created before that change.
ALTER TABLE tenant ADD COLUMN IF NOT EXISTS name     TEXT;
ALTER TABLE tenant ADD COLUMN IF NOT EXISTS company  TEXT;
ALTER TABLE tenant ADD COLUMN IF NOT EXISTS location TEXT;
ALTER TABLE tenant ADD COLUMN IF NOT EXISTS bio      TEXT;
