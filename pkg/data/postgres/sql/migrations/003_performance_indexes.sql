-- Performance indexes for common query patterns

-- PR-to-review self-joins in insight queries (org, repo, number)
CREATE INDEX IF NOT EXISTS idx_event_org_repo_number ON event (org, repo, number);

-- Developer RLS policy: EXISTS join from event to tenant_repo via username
CREATE INDEX IF NOT EXISTS idx_event_username_org_repo ON event (username, org, repo);

-- Covering index for event self-joins filtered by type (time-to-first-response, unanswered rate)
CREATE INDEX IF NOT EXISTS idx_event_org_repo_number_type ON event (org, repo, number, type, created_at);

-- Enrichment batch queries: WHERE entity IS NULL
CREATE INDEX IF NOT EXISTS idx_developer_entity_null ON developer (username) WHERE entity IS NULL;
