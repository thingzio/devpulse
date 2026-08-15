-- DevPulse base schema (squashed from migrations 001-014, 2026-04-25).

CREATE TABLE IF NOT EXISTS devpulse_developer (
    username TEXT NOT NULL,
    full_name TEXT NOT NULL,
    email TEXT,
    avatar TEXT,
    url TEXT,
    entity TEXT,
    reputation REAL,
    reputation_updated_at TEXT,
    PRIMARY KEY (username)
);

CREATE TABLE IF NOT EXISTS devpulse_event (
    org TEXT NOT NULL,
    repo TEXT NOT NULL,
    username TEXT NOT NULL,
    type TEXT NOT NULL,
    date TEXT NOT NULL,
    url TEXT NOT NULL,
    mentions TEXT NOT NULL,
    labels TEXT NOT NULL,
    state TEXT,
    number INTEGER,
    created_at TEXT,
    closed_at TEXT,
    merged_at TEXT,
    additions INTEGER,
    deletions INTEGER,
    title TEXT NOT NULL DEFAULT '',
    changed_files INTEGER,
    commits INTEGER,
    PRIMARY KEY (org, repo, username, type, date),
    FOREIGN KEY(username) REFERENCES devpulse_developer(username) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS devpulse_repo_meta (
    org TEXT NOT NULL,
    repo TEXT NOT NULL,
    stars INTEGER NOT NULL DEFAULT 0,
    forks INTEGER NOT NULL DEFAULT 0,
    open_issues INTEGER NOT NULL DEFAULT 0,
    language TEXT,
    license TEXT,
    archived INTEGER NOT NULL DEFAULT 0,
    updated_at TEXT NOT NULL DEFAULT '',
    last_import_at TEXT NOT NULL DEFAULT '',
    has_coc INTEGER NOT NULL DEFAULT 0,
    has_contributing INTEGER NOT NULL DEFAULT 0,
    has_readme INTEGER NOT NULL DEFAULT 0,
    has_issue_template INTEGER NOT NULL DEFAULT 0,
    has_pr_template INTEGER NOT NULL DEFAULT 0,
    community_health_pct INTEGER NOT NULL DEFAULT 0,
    pushed_at TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (org, repo)
);

CREATE TABLE IF NOT EXISTS devpulse_release (
    org TEXT NOT NULL,
    repo TEXT NOT NULL,
    tag TEXT NOT NULL,
    name TEXT,
    published_at TEXT,
    prerelease INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (org, repo, tag)
);

CREATE TABLE IF NOT EXISTS devpulse_release_asset (
    org TEXT NOT NULL,
    repo TEXT NOT NULL,
    tag TEXT NOT NULL,
    name TEXT NOT NULL,
    content_type TEXT NOT NULL DEFAULT '',
    size INTEGER NOT NULL DEFAULT 0,
    download_count INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (org, repo, tag, name)
);

CREATE TABLE IF NOT EXISTS devpulse_container_version (
    org TEXT NOT NULL,
    repo TEXT NOT NULL,
    package TEXT NOT NULL,
    version_id INTEGER NOT NULL,
    tag TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    PRIMARY KEY (org, repo, package, version_id)
);

CREATE TABLE IF NOT EXISTS devpulse_repo_metric_history (
    org TEXT NOT NULL,
    repo TEXT NOT NULL,
    date TEXT NOT NULL,
    stars INTEGER NOT NULL DEFAULT 0,
    forks INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (org, repo, date)
);

CREATE TABLE IF NOT EXISTS devpulse_repo_insights (
    org TEXT NOT NULL,
    repo TEXT NOT NULL,
    insights_json TEXT NOT NULL,
    period_months INTEGER NOT NULL DEFAULT 3,
    model TEXT NOT NULL DEFAULT '',
    generated_at TEXT NOT NULL,
    event_count INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (org, repo)
);

CREATE TABLE IF NOT EXISTS devpulse_state (
    query TEXT NOT NULL,
    org TEXT NOT NULL,
    repo TEXT NOT NULL,
    page INTEGER NOT NULL,
    since INTEGER NOT NULL,
    backfill_until INTEGER,
    PRIMARY KEY (query, org, repo)
);

CREATE TABLE IF NOT EXISTS devpulse_sub (
    type TEXT NOT NULL,
    old TEXT NOT NULL,
    new TEXT NOT NULL,
    PRIMARY KEY (type, old)
);

-- ─── Indexes ────────────────────────────────────────────────────────

CREATE INDEX IF NOT EXISTS idx_devpulse_event_org_repo_date ON devpulse_event (org, repo, date);
CREATE INDEX IF NOT EXISTS idx_devpulse_event_org_repo_type_date ON devpulse_event (org, repo, type, date);
CREATE INDEX IF NOT EXISTS idx_devpulse_event_org_repo_created_at ON devpulse_event (org, repo, created_at);
CREATE INDEX IF NOT EXISTS idx_devpulse_developer_reputation ON devpulse_developer (reputation);

CREATE INDEX IF NOT EXISTS idx_devpulse_event_org_repo_number ON devpulse_event (org, repo, number);
CREATE INDEX IF NOT EXISTS idx_devpulse_event_username_org_repo ON devpulse_event (username, org, repo);
CREATE INDEX IF NOT EXISTS idx_devpulse_event_org_repo_number_type ON devpulse_event (org, repo, number, type, created_at);
CREATE INDEX IF NOT EXISTS idx_devpulse_developer_entity_null ON devpulse_developer (username) WHERE entity IS NULL;

-- Performance indexes (from migration 014)
CREATE INDEX IF NOT EXISTS idx_devpulse_event_org_repo_type_created
    ON devpulse_event (org, repo, type, created_at);
CREATE INDEX IF NOT EXISTS idx_devpulse_event_org_repo_date_user
    ON devpulse_event (org, repo, date, username);
CREATE INDEX IF NOT EXISTS idx_devpulse_developer_entity
    ON devpulse_developer (entity)
    WHERE entity IS NOT NULL AND entity != '';
CREATE INDEX IF NOT EXISTS idx_devpulse_release_org_repo_published
    ON devpulse_release (org, repo, published_at);
CREATE INDEX IF NOT EXISTS idx_devpulse_event_open_prs
    ON devpulse_event (org, repo, created_at)
    WHERE type = 'pr' AND (state IS NULL OR state NOT IN ('merged', 'closed'));
CREATE INDEX IF NOT EXISTS idx_devpulse_event_date
    ON devpulse_event (date);
