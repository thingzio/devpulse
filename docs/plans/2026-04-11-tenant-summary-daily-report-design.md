# Tenant Summary & Daily Report

## Problem

`tools/metrics-review` covers infrastructure health (Cloud Run, Cloud SQL, GCP metrics) but lacks business/growth visibility: tenant count, repo growth, contributor trends, import errors. No way to track these numbers over time with DoD/WoW/MoM comparisons.

## Solution

Three new capabilities on the admin service:

1. **`GET /summary`** — returns structured JSON with platform counts, historical deltas, and error repos
2. **`POST /report`** — orchestrates summary + metrics analysis, sends daily email via SendGrid
3. **`tools/tenant-summary`** — shell script for on-demand terminal-formatted summary

Rename `GET /metrics/review` to `GET /metrics` for consistency.

## Database Schema

New `platform_stats` table (one row per day, upsert-idempotent):

```sql
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
```

Upsert via `INSERT ... ON CONFLICT (date) DO UPDATE SET ...` — multiple calls per day are safe, last write wins.

## Endpoints

### `GET /summary`

1. Queries DB for current aggregate counts (tenants by plan, active repos, total events, unique non-bot contributors, active installations, repos with errors > 0)
2. Upserts today's row into `platform_stats`
3. Reads rows for today, yesterday (DoD), 7 days ago (WoW), 30 days ago (MoM)
4. Computes absolute and percentage deltas
5. Queries repos with `import_errors > 0` for error details

Response shape:

```json
{
  "date": "2026-04-11",
  "current": {
    "tenants": 42, "tenants_free": 30, "tenants_starter": 8,
    "tenants_pro": 3, "tenants_enterprise": 1,
    "repos": 156, "events": 89432, "contributors": 1203,
    "installations": 5, "repos_with_errors": 2
  },
  "dod": { "tenants": 1, "tenants_pct": 2.4, "repos": 3, ... },
  "wow": { "tenants": 5, "tenants_pct": 13.5, ... },
  "mom": { "tenants": 12, "tenants_pct": 40.0, ... },
  "error_repos": [
    { "org": "foo", "repo": "bar", "errors": 3, "last_error": "rate limit exceeded" }
  ],
  "updated_at": "2026-04-11T14:30:00Z"
}
```

Missing historical rows produce null deltas (not zeros).

### `GET /metrics` (renamed from `/metrics/review`)

No behavior change — only the route path changes.

### `POST /report`

1. Calls summary logic internally (upserts snapshot, gets structured data)
2. Calls metrics logic internally (collects GCP metrics, gets Claude analysis)
3. Composes HTML email with two sections:
   - **Platform Summary**: counts table with DoD/WoW/MoM deltas and directional arrows, error repos
   - **Infrastructure Analysis**: Claude narrative from GCP metrics
4. Sends via SendGrid API
5. Returns `{"sent": true}` or error

Plain text fallback included for non-HTML email clients.

Subject: `DevPulse Daily Report — 2026-04-11`

## Config

**Secret (Secret Manager):**
- `sendgrid-api-key` — SendGrid API token only

**Env vars (Cloud Run):**
- `REPORT_TO_EMAIL` — recipient email address
- `REPORT_FROM_EMAIL` — sender email (must be verified in SendGrid)
- `REPORT_SUBJECT_PREFIX` — defaults to "DevPulse Daily Report"

If `SENDGRID_API_KEY` is missing, `POST /report` returns 503. `GET /summary` works independently.

## Scripts

### `tools/tenant-summary` (new)

Calls `GET /summary`, formats JSON as terminal table. Same auth pattern as existing tools (gcloud identity token + terraform admin_url output).

### `tools/metrics-review` (update)

Change endpoint from `/metrics/review` to `/metrics`.

## Terraform Changes

- Add `report-config` secret
- Mount secret on admin service
- Add Cloud Scheduler job: daily `POST /report` with IAM auth

## Error Tracking

`import_errors` on `tenant_repo` already tracks consecutive failures (resets to 0 on success). The summary reports repos with any `import_errors > 0`, including error count and last error message.

## Files Changed

| File | Change |
|------|--------|
| `pkg/admin/admin.go` | Add routes, pass `*sql.DB` to handlers |
| `pkg/admin/metrics.go` | Rename handler for `/metrics` |
| `pkg/admin/summary.go` | New: snapshot upsert, delta computation, JSON response |
| `pkg/admin/report.go` | New: orchestrator, email composition, SendGrid send |
| `pkg/admin/types.go` | Add summary/report response types |
| `pkg/data/postgres/sql/migrations_saas/` | New migration for `platform_stats` |
| `tools/tenant-summary` | New script |
| `tools/metrics-review` | Update endpoint path |
| `infra/saas/*.tf` | Secret, scheduler, env var mount |
