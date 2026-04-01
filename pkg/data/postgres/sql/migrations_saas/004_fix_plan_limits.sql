-- Re-apply plan limits (003 may have been recorded without executing)
UPDATE tenant SET max_repos = 3, max_events_per_week = 1000, updated_at = NOW()
WHERE plan = 'free' AND (max_repos != 3 OR max_events_per_week != 1000);

UPDATE tenant SET max_repos = 15, max_events_per_week = 15000, updated_at = NOW()
WHERE plan = 'pro' AND (max_repos != 15 OR max_events_per_week != 15000);

UPDATE tenant SET max_repos = 0, max_events_per_week = 0, updated_at = NOW()
WHERE plan = 'enterprise' AND (max_repos != 0 OR max_events_per_week != 0);
