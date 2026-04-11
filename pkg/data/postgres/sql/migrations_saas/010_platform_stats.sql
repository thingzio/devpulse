CREATE TABLE IF NOT EXISTS platform_stats (
    date                DATE PRIMARY KEY,
    tenants             INT NOT NULL DEFAULT 0,
    tenants_free        INT NOT NULL DEFAULT 0,
    tenants_starter     INT NOT NULL DEFAULT 0,
    tenants_pro         INT NOT NULL DEFAULT 0,
    tenants_enterprise  INT NOT NULL DEFAULT 0,
    repos               INT NOT NULL DEFAULT 0,
    events              BIGINT NOT NULL DEFAULT 0,
    contributors        INT NOT NULL DEFAULT 0,
    installations       INT NOT NULL DEFAULT 0,
    repos_with_errors   INT NOT NULL DEFAULT 0,
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
