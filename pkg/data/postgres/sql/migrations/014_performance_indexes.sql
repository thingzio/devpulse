-- Performance indexes for query optimization (2026-04-16)

-- Covers: time-to-merge, time-to-close, PR size, review latency, aging PRs, change failures
-- Queries filter on (org, repo, type) + range scan on created_at
CREATE INDEX IF NOT EXISTS idx_devpulse_event_org_repo_type_created
    ON devpulse_event (org, repo, type, created_at);

-- Covers: bus factor, retention, momentum, daily activity, signals
-- Adds username for index-only scans on aggregate GROUP BY queries
-- Supersedes idx_devpulse_event_org_repo_date (strict prefix match preserved)
CREATE INDEX IF NOT EXISTS idx_devpulse_event_org_repo_date_user
    ON devpulse_event (org, repo, date, username);

-- Covers: entity percentage, pony factor, developer percentage queries
-- Partial: only rows with actual entity values (keeps index small)
CREATE INDEX IF NOT EXISTS idx_devpulse_developer_entity
    ON devpulse_developer (entity)
    WHERE entity IS NOT NULL AND entity != '';

-- Covers: change failure rate, time-to-restore-bugs EXISTS subquery
-- Release table currently only has PK on (org, repo, tag)
CREATE INDEX IF NOT EXISTS idx_devpulse_release_org_repo_published
    ON devpulse_release (org, repo, published_at);

-- Covers: aging PRs query (tiny subset of event table)
CREATE INDEX IF NOT EXISTS idx_devpulse_event_open_prs
    ON devpulse_event (org, repo, created_at)
    WHERE type = 'pr' AND (state IS NULL OR state NOT IN ('merged', 'closed'));
