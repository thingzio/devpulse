-- idx_devpulse_event_username (username) is fully subsumed by two existing
-- composite indexes that lead with the same column:
--
--   idx_devpulse_event_username_org_repo (username, org, repo)  [this chain]
--   idx_devpulse_event_username_date     (username, date)       [saas chain]
--
-- Any plan that could use the single-column index can use either composite
-- index's leading column instead, so it contributes nothing but write
-- amplification and storage on the largest table in the schema.

DROP INDEX IF EXISTS idx_devpulse_event_username;
