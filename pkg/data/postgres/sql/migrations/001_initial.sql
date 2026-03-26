-- DevPulse base schema

CREATE TABLE IF NOT EXISTS developer (
    username TEXT NOT NULL,
    full_name TEXT NOT NULL,
    email TEXT,
    avatar TEXT,
    url TEXT,
    entity TEXT,
    reputation REAL,
    reputation_updated_at TEXT,
    reputation_deep INTEGER DEFAULT 0,
    reputation_signals TEXT,
    PRIMARY KEY (username)
);

CREATE TABLE IF NOT EXISTS event (
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
    FOREIGN KEY(username) REFERENCES developer(username) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS repo_meta (
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
    PRIMARY KEY (org, repo)
);

CREATE TABLE IF NOT EXISTS release (
    org TEXT NOT NULL,
    repo TEXT NOT NULL,
    tag TEXT NOT NULL,
    name TEXT,
    published_at TEXT,
    prerelease INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (org, repo, tag)
);

CREATE TABLE IF NOT EXISTS release_asset (
    org TEXT NOT NULL,
    repo TEXT NOT NULL,
    tag TEXT NOT NULL,
    name TEXT NOT NULL,
    content_type TEXT NOT NULL DEFAULT '',
    size INTEGER NOT NULL DEFAULT 0,
    download_count INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (org, repo, tag, name)
);

CREATE TABLE IF NOT EXISTS container_version (
    org TEXT NOT NULL,
    repo TEXT NOT NULL,
    package TEXT NOT NULL,
    version_id INTEGER NOT NULL,
    tag TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    PRIMARY KEY (org, repo, package, version_id)
);

CREATE TABLE IF NOT EXISTS repo_metric_history (
    org TEXT NOT NULL,
    repo TEXT NOT NULL,
    date TEXT NOT NULL,
    stars INTEGER NOT NULL DEFAULT 0,
    forks INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (org, repo, date)
);

CREATE TABLE IF NOT EXISTS repo_insights (
    org TEXT NOT NULL,
    repo TEXT NOT NULL,
    insights_json TEXT NOT NULL,
    period_months INTEGER NOT NULL DEFAULT 3,
    model TEXT NOT NULL DEFAULT '',
    generated_at TEXT NOT NULL,
    PRIMARY KEY (org, repo)
);

CREATE TABLE IF NOT EXISTS state (
    query TEXT NOT NULL,
    org TEXT NOT NULL,
    repo TEXT NOT NULL,
    page INTEGER NOT NULL,
    since INTEGER NOT NULL,
    PRIMARY KEY (query, org, repo)
);

CREATE TABLE IF NOT EXISTS sub (
    type TEXT NOT NULL,
    old TEXT NOT NULL,
    new TEXT NOT NULL,
    PRIMARY KEY (type, old)
);

CREATE INDEX IF NOT EXISTS idx_event_org_repo_date ON event (org, repo, date);
CREATE INDEX IF NOT EXISTS idx_event_org_repo_type_date ON event (org, repo, type, date);
CREATE INDEX IF NOT EXISTS idx_event_org_repo_created_at ON event (org, repo, created_at);
CREATE INDEX IF NOT EXISTS idx_event_username ON event (username);
CREATE INDEX IF NOT EXISTS idx_developer_reputation ON developer (reputation);
