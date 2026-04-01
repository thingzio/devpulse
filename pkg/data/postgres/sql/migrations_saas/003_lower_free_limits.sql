-- Lower free plan defaults: 3 repos, 1000 events/week
ALTER TABLE tenant ALTER COLUMN max_repos SET DEFAULT 3;
ALTER TABLE tenant ALTER COLUMN max_events_per_week SET DEFAULT 1000;

-- Update existing free tenants to new limits
UPDATE tenant SET max_repos = 3, max_events_per_week = 1000, updated_at = NOW()
WHERE plan = 'free' AND (max_repos = 5 OR max_events_per_week = 2000);

-- Update existing pro tenants to new limits
UPDATE tenant SET max_repos = 15, max_events_per_week = 15000, updated_at = NOW()
WHERE plan = 'pro' AND (max_repos = 25 OR max_events_per_week = 20000);
