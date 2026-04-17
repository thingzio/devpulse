-- Index for portfolio-level queries that filter by date without org/repo.
-- Summary, bus factor, pony factor, and banner stats all scan devpulse_event
-- with WHERE date >= $1 across all repos. Existing indexes start with
-- (org, repo, ...) and require a full seq scan for date-only filters.

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_devpulse_event_date
    ON devpulse_event (date);
