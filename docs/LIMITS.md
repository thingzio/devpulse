# Import Throughput and Rate Limits

This document describes the import system's throughput characteristics, GitHub API rate limits, and scaling considerations.

## Architecture

The import worker runs as a Cloud Run job on an hourly schedule (`0 * * * *`). It uses a PostgreSQL-backed SKIP LOCKED claim queue so multiple concurrent tasks safely share work without overlap.

| Setting | Value |
|---------|-------|
| Schedule | Hourly (`0 * * * *`) |
| Job timeout | 3600s (1 hour) |
| Parallelism | 3 (task_count and parallelism) |
| Queue mechanism | `SELECT ... FOR UPDATE SKIP LOCKED` |

## GitHub API Budget

Each tenant's GitHub App installation gets its own **5,000 requests/hour** budget. Tenants are fully isolated — one tenant's API usage never affects another.

| Auth method | Rate limit |
|-------------|-----------|
| Installation token (production) | 5,000/hr per installation |
| Personal access token (dev) | 5,000/hr per token |
| Unauthenticated (fallback) | 60/hr per IP |

## API Calls Per Repo Import

Each repo runs 7 import phases:

| Phase | API Calls (incremental) | API Calls (first import) | Notes |
|-------|------------------------:|-------------------------:|-------|
| Metadata | 2 | 2 | `Repositories.Get` + `GetCommunityHealthMetrics` |
| Events (5 concurrent) | 10–30 | 50–200+ | PRs, reviews, issues, comments, forks — paginated at 100/page |
| PR size backfill | 0–20 | 50–200 | `PullRequests.Get` per new PR |
| Releases | 1–3 | 1–5 | `ListReleases` paginated |
| Metric history | 3–10 | 5–15 | Stars + forks pagination |
| Containers | 0–5 | 2–10 | Packages + versions |
| Reputation | 5–10/dev | 5–10/dev | User profile + org membership + search queries |

### Typical Totals

| Scenario | API calls/repo |
|----------|---------------|
| Incremental (low activity) | ~30–50 |
| Incremental (moderate, ~20 PRs/week) | ~60–100 |
| Incremental (high activity) | ~200–400 |
| First import (moderate repo) | ~150–300 |
| First import (large repo, 500+ PRs) | ~500+ |

## Throughput Per Tenant

With 5,000 API requests/hour per installation token:

| Repo activity | Repos/hour (per tenant) |
|---------------|------------------------|
| Low activity (incremental) | ~100–160 |
| Moderate activity (incremental) | ~50–80 |
| High activity (incremental) | ~12–25 |
| First import (moderate) | ~15–30 |

The free tier (5 repos) and pro tier (25 repos) are well within these bounds for incremental imports. First imports of large repos may span multiple hourly cycles — the importer resumes from saved page state automatically.

## Scaling Characteristics

### What scales linearly with tenants

- **GitHub API budget** — each tenant's installation token has its own 5,000/hr
- **Queue work distribution** — SKIP LOCKED ensures no repo is claimed twice

### What doesn't scale automatically

| Bottleneck | Symptom | Mitigation |
|------------|---------|------------|
| Serial processing | Job exceeds 1hr timeout | Increase `import_parallelism` |
| DB write contention | Slow event flushes, lock waits | Increase Cloud SQL tier, tune pool |
| Connection pool | `too many clients` errors | Increase pool size in DATABASE_URL |
| Cloud SQL CPU | High latency on upserts | Scale to `db-custom-*` tier |

### Recommended settings by scale

| Tenants | Repos (est.) | `import_parallelism` | DB tier | Pool size |
|---------|-------------|---------------------|---------|-----------|
| 1–10 | 5–100 | 3 | db-f1-micro | 3/2 |
| 10–50 | 50–500 | 3–5 | db-g1-small | 10/5 |
| 50–200 | 250–2000 | 5–10 | db-custom-2-4096 | 20/10 |

## Rate Limit Handling

### Primary Rate Limit

The importer tracks remaining requests via `X-RateLimit-Remaining` response headers. When remaining requests drop below 10, it sleeps until reset (with jitter). Maximum wait is capped at 15 minutes — longer waits return an error.

### Secondary (Abuse) Rate Limits

| Limit | Threshold |
|-------|-----------|
| Concurrent requests | 100 |
| REST API points | 900/minute |
| CPU time | 90s per 60s real time |

Secondary limits return HTTP 403 with `Retry-After`. The importer detects `AbuseRateLimitError` in the PR detail backfill loop and retries after the specified wait period (default: 60s).

### Summary

| Limit Type | Detection | Response |
|------------|-----------|----------|
| Primary (approaching) | `Rate.Remaining <= 10` | Warn + sleep until reset + jitter |
| Primary (exhausted) | `Rate.Remaining == 0` | Warn + sleep until reset + jitter |
| Primary (reset too far) | Wait > 15 minutes | Return error, repo re-enters queue |
| Secondary (abuse) | `AbuseRateLimitError` | Warn + sleep for `Retry-After`, retry once |
