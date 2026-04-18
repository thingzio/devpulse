-- 019: Token quota time-series samples.
-- Records GitHub API rate limit snapshots per installation during import runs.

CREATE TABLE IF NOT EXISTS devpulse_token_quota_sample (
    id              BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    sampled_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    installation_id BIGINT NOT NULL,
    login           TEXT NOT NULL,
    quota_limit     INT NOT NULL,
    quota_used      INT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_devpulse_token_quota_sample_time
    ON devpulse_token_quota_sample(sampled_at DESC);

-- Auto-purge samples older than 30 days to bound table growth.
-- Run periodically via: DELETE FROM devpulse_token_quota_sample WHERE sampled_at < NOW() - INTERVAL '30 days';
