-- Wipe fork rows so the next scheduled import re-creates them with stable
-- created_at-based dates. The previous importer keyed fork rows on the
-- forker's UpdatedAt, which shifts every time the forker pushes to their
-- fork — accumulating one row per push-day. Existing rows have NULL
-- created_at (the importer didn't populate it), so there's no way to
-- rebase the date column in place; a wipe + re-import is the cleanest
-- path. The fork chart on the dashboard goes blank until the next import
-- cycle (every ~2 hours) repopulates it.

DELETE FROM devpulse_event WHERE type = 'fork';
