# Infrastructure

DevPulse runs on GCP: Cloud Run for compute, Cloud SQL (PostgreSQL) for storage, Cloud Scheduler for periodic imports.

## Architecture

```
Internet
   |
   +-- Cloud Run service (serve mode)
   |     +-- OAuth sign-in
   |     +-- Dashboard (Chart.js)
   |     +-- Webhook endpoint (GitHub App)
   |     +-- Data API (30+ chart endpoints)
   |
   +-- Cloud Run job (import, every 2 hours)
   |     +-- Per-tenant repo import via GitHub API
   |     +-- LLM insights generation (Claude Haiku 4.5)
   |
   +-- Cloud Run service (admin, IAM-gated)
   |     +-- Tenant plan management (upgrade/downgrade)
   |
   +-- GitHub
         +-- OAuth (user identity)
         +-- App webhooks (installation events)
         +-- API (events, metadata, releases)

Cloud SQL PostgreSQL (shared instance: thingzio-pg, database: thingz)
   +-- Base tables (event, developer, repo_meta, release, ...)
   +-- SaaS tables (tenant, session, tenant_repo, ...)
   +-- RLS policies (tenant isolation)

Cloud Scheduler -> triggers import job every 2 hours
Secret Manager  -> GitHub App key, OAuth secret, webhook secret, Anthropic API key
Cloudflare DNS  -> devpulse.thingz.io
```

## Compute

Two container images: `devpulse-site` (Cloud Run service, includes admin dashboard), `devpulse-import` (Cloud Run job).

| Mode | Deployment | Scaling | Access |
|------|-----------|---------|--------|
| Serve | Cloud Run service | 0-10 instances (scale-to-zero) | Public |
| Import | Cloud Run job | Every 2 hours, parallelism=3 (deterministic sharding), full pipeline | Internal |
| Admin | Cloud Run service | 0-1 instances, scale-to-zero | IAM-gated |

The serve service runs with `min_instance_count=0` (scale-to-zero) to minimize cost. Cold starts for the Go binary are ~2s, within the 1s p99 latency alert threshold. The import job runs for the duration of the import and exits. The admin service is IAM-protected (`roles/run.invoker`) and scales to zero when idle.

## Response Caching

Dashboard data only changes at import time (every 2 hours). Two-layer caching eliminates redundant DB queries:

**Server-side (in-memory):** `sync.Map` with 5-minute TTL, keyed by `(tenant_id, path, query_params)`. Protects the DB from concurrent requests across all users viewing the same data. Covers all 25+ insight endpoints via the `insightHandler` and `insightWithEntityHandler` factories. Memory footprint: ~50KB per active tenant (~4MB at 80 tenants).

**Browser-side:** `Cache-Control: private, max-age=1800` on all `/data/` responses. Prevents repeat fetches on tab switches, page reloads, and back-button navigation. `private` ensures CDNs don't cache tenant-scoped data.

Combined effect: first request in a 5-minute window hits the DB; subsequent requests served from memory (sub-millisecond). Browser won't re-request for 30 minutes. Worst-case staleness is ~40 minutes (visit 10 min before import + 30 min browser cache), well within the hourly refresh cadence.

**Excluded from caching:** `/data/export/csv`, `/data/search` (POST), `/data/developer/search` (autocomplete).

## Anthropic API (LLM Insights)

The import worker calls the Claude Haiku 4.5 Messages API (`claude-haiku-4-5-20251001`) to generate per-repo insights during each import. The call is gated by the tenant's plan AI level:

| Plan | AI Level | Behavior |
|------|----------|----------|
| Free | 0 | No LLM calls |
| Starter | 1 | 1 insight call per repo per import |
| Pro | 2 | 1 insight call per repo per import (richer analysis) |

Each call sends ~2-4K input tokens (JSON metrics payload + DORA benchmarks + instructions) and receives ~500-1K output tokens (structured JSON with 5 observations + 3 actions). Max output capped at 4,096 tokens.

**Per-call cost estimate** (Claude Haiku 4.5 pricing: $0.80/MTok input, $4.00/MTok output):
- Input: ~3K tokens = ~$0.0024
- Output: ~750 tokens = ~$0.003
- **~$0.005 per repo per call**

**Caching:** Insights are regenerated only when both gates pass: (1) last generation > 7 days ago, and (2) event count changed by > 10%. This reduces calls from ~720/day to ~4-5/week per repo for active repos, and near-zero for quiet repos. Effective cost: **~$0.02/repo/month** (vs $0.17 without caching).

## Current Cost (Actual)

Current production setup: `db-g1-small`, 3 Cloud Run deployments, ~5 tenants.

| Service | Details | Estimate |
|---------|---------|----------|
| Cloud SQL | db-g1-small, shared vCPU, 1.7GB RAM, 10GB storage | $27/mo |
| Cloud Run Service (serve) | scale-to-zero (min=0), 1 vCPU/512MB | $5/mo |
| Cloud Run Job (import) | every 2 hours, ~3 tasks, ~10 min/run | $3.50/mo |
| Cloud Run Service (admin) | scale-to-zero, 1 vCPU/512MB | $0.50/mo |
| Anthropic API | Claude Haiku 4.5, ~15 repos (cached, weekly regen) | $0.30/mo |
| Cloud Scheduler | 1 job every 2 hours | free (3 free) |
| Secret Manager | 5 secrets, ~2K accesses/mo | free tier |
| Artifact Registry | standard repo, <1GB, 7-day untagged cleanup | $0.10/mo |
| Cloud DNS | 1 hosted zone | $0.20/mo |
| Cloud Monitoring | log-based metrics, 11 alert policies, email | free tier |
| **Total** | | **~$37/mo** |

## Cost by Tenant Scale

The tables below estimate monthly costs at different tenant counts. Plan mix assumptions: 60% Free, 25% Starter, 15% Pro. Repos per tenant use plan maximums as upper bound (actual usage is typically 40-60% of limit). Anthropic costs reflect insight caching (7-day age gate + 10% event delta gate).

### 25 Tenants

15 Free (1 repo each), 6 Starter (avg 3 repos), 4 Pro (avg 15 repos).
Total repos: ~93. Paid repos with AI: ~78.

| Service | Details | Estimate |
|---------|---------|----------|
| Cloud SQL | db-g1-small, ~15GB storage | $29/mo |
| Cloud Run (serve) | always-on, light load | $15/mo |
| Cloud Run (import) | hourly, ~15 min/run | $6/mo |
| Cloud Run (admin) | scale-to-zero | $0.50/mo |
| Anthropic API | ~78 repos x $0.02/mo (cached) | $1.50/mo |
| Fixed (scheduler, DNS, secrets, AR, monitoring) | | $1/mo |
| **Total** | | **~$53/mo** |

### 100 Tenants

60 Free (1 repo each), 25 Starter (avg 3 repos), 15 Pro (avg 15 repos).
Total repos: ~360. Paid repos with AI: ~300.

| Service | Details | Estimate |
|---------|---------|----------|
| Cloud SQL | db-g1-small, ~25GB storage | $31/mo |
| Cloud Run (serve) | always-on, moderate load | $18/mo |
| Cloud Run (import) | hourly, ~30 min/run | $11/mo |
| Cloud Run (admin) | scale-to-zero | $0.50/mo |
| Anthropic API | ~300 repos x $0.02/mo (cached) | $6/mo |
| Fixed | | $1/mo |
| **Total** | | **~$68/mo** |

**Upgrade signal:** DB CPU sustained > 80%, or import duration > 30 minutes.

### 300 Tenants

180 Free (1 repo each), 75 Starter (avg 3 repos), 45 Pro (avg 15 repos).
Total repos: ~1,080. Paid repos with AI: ~900.

| Service | Details | Estimate |
|---------|---------|----------|
| Cloud SQL | db-custom-1-3840, 1 vCPU, 3.75GB, ~50GB storage | $59/mo |
| Cloud Run (serve) | always-on, higher concurrency | $25/mo |
| Cloud Run (import) | parallelism=5, ~45 min/run | $20/mo |
| Cloud Run (admin) | scale-to-zero | $0.50/mo |
| PgBouncer sidecar | connection pooling | $10/mo |
| Anthropic API | ~900 repos x $0.02/mo (cached) | $18/mo |
| Fixed | | $1/mo |
| **Total** | | **~$134/mo** |

**Upgrade signal:** connection count approaching limits, query latency > 500ms.

### 1,000 Tenants

600 Free (1 repo each), 250 Starter (avg 3 repos), 150 Pro (avg 15 repos).
Total repos: ~3,600. Paid repos with AI: ~3,000.

| Service | Details | Estimate |
|---------|---------|----------|
| AlloyDB primary | 2 vCPU, ~100GB storage | $185/mo |
| AlloyDB read pool (optional) | 2 vCPU | $150/mo |
| Cloud Run (serve) | always-on, 2-5 instances avg | $50/mo |
| Cloud Run (import) | parallelism=10+, Cloud Tasks | $33/mo |
| Cloud Run (admin) | scale-to-zero | $0.50/mo |
| Cloud Tasks | parallel import dispatch | $10/mo |
| Anthropic API | ~3,000 repos x $0.02/mo (cached) | $60/mo |
| Fixed | | $1/mo |
| **Total** | | **~$490/mo** |

### Cost Scaling Summary

| Tenants | DB Tier | Repos (est) | Anthropic | Total |
|---------|---------|-------------|-----------|-------|
| 5 (current) | db-g1-small | ~15 | $0.30 | ~$37 |
| 25 | db-g1-small | ~93 | $1.50 | ~$53 |
| 100 | db-g1-small | ~360 | $6 | ~$68 |
| 300 | db-custom-1-3840 | ~1,080 | $18 | ~$134 |
| 1,000 | AlloyDB | ~3,600 | $60 | ~$490 |

With insight caching, the Anthropic API drops from the dominant cost to a minor line item (~12% at 1K tenants vs ~55% without caching). The database and compute are now the primary cost drivers at scale. Key cost levers:
1. **AI gating by plan** — Free tenants generate zero LLM cost
2. **Insight caching** — 7-day age gate + 10% event delta gate reduces LLM calls by ~85%
3. **Incremental imports** — only new data fetched from GitHub API

## Database Scaling Plan

The shared Cloud SQL instance (`thingzio-pg`) is owned by the `thingzio/infra` repo. DB tier changes are made there, not in DevPulse Terraform. Each Cloud SQL tier change is a Terraform apply with zero downtime (with HA) or ~1-3 minutes (without). The AlloyDB migration is a planned maintenance event.

### AlloyDB Migration (1,000+ tenants)

Why AlloyDB at this tier:
- **Connection pooling built-in** — no PgBouncer sidecar needed
- **Columnar engine** — dashboard aggregation queries (GROUP BY month, contributor counts) run 5-10x faster
- **Read pool** — dashboard reads separated from import writes, shared storage (no replication lag)

Migration: `pg_dump`/`pg_restore` (minutes of downtime) or Database Migration Service (seconds). No code changes — same PostgreSQL wire protocol, same `lib/pq` driver. Update `DATABASE_URL` and swap Cloud SQL Auth Proxy for AlloyDB Auth Proxy in Terraform.

## Import Worker Scaling

The import job uses deterministic task-index sharding. Each Cloud Run task fetches the full sorted repo list, takes every Nth item based on its task index, and processes its shard using goroutine workers. No DB coordination needed — sharding is computed in-process. Current setting: `parallelism=3`, `workers=2`.

All upsert batches are sorted by primary key before execution to ensure consistent lock acquisition order across parallel tasks, preventing deadlocks. This applies to: developers (by `username`), events (by `org, repo, username, type, date`), releases (by `tag`), release assets (by `name`), and metric history (by `date`).

| Tenants | ~Repos | `import_parallelism` | Est. Duration | Strategy |
|---------|--------|---------------------|--------------|----------|
| 1-10 | 5-100 | 3 (current) | < 15 min | Current settings |
| 10-50 | 50-500 | 3-5 | 15-30 min | Bump parallelism, increase DB pool |
| 50-200 | 250-2000 | 5-10 | 30-50 min | Scale DB tier + pool |
| 200+ | 2000+ | 10+ | Variable | Cloud Tasks for unbounded parallelism |

Cloud Tasks migration path (for 200+ tenants):
1. Cloud Scheduler triggers a dispatcher endpoint
2. Dispatcher queries unclaimed repos and enqueues one Cloud Task per repo
3. Each task claims and imports a single repo
4. Unbounded parallelism, no timeout concern

### GitHub API Budget

Each tenant's GitHub App installation gets its own **5,000 requests/hour** budget. Tenants are fully isolated — one tenant's API usage never affects another.

| Auth method | Rate limit |
|-------------|-----------|
| Installation token (production) | 5,000/hr per installation |
| Personal access token (dev) | 5,000/hr per token |
| Unauthenticated (fallback) | 60/hr per IP |

### API Calls Per Repo Import

Each repo runs up to 8 import phases (progressive backfill model):

| Phase | Incremental | First import | Notes |
|-------|------------:|-------------:|-------|
| Metadata | 2 | 2 | `Repositories.Get` + `GetCommunityHealthMetrics` |
| Fresh events (5 concurrent) | 5-20 | 30-100+ | 21-day window (configurable via `IMPORT_FRESH_DAYS`), 100/page |
| Releases | 1-3 | 1-5 | `ListReleases` paginated |
| Metric history | 3-10 | 5-15 | Stars + forks pagination |
| Containers | 0-5 | 2-10 | Packages + versions |
| Reputation | 0-5 | 5-20 | Basic scoring from event data |
| Insights | 1 | 1 | Anthropic API call (not GitHub) |
| Backfill events | 5-15 | 20-80 | 7-day chunk (configurable via `IMPORT_BACKFILL_CHUNK_DAYS`) |

DB writes use sub-batches of `IMPORT_DB_BATCH_SIZE` (default 100) events per transaction, keeping write times flat (~1-3s) regardless of repo size.

Typical totals: **~40-80 calls/repo** incremental, **~100-250** first import. The free tier (1 repo) and starter tier (5 repos) are well within the 5,000/hr budget.

### Throughput Per Tenant

| Repo activity | Repos/hour |
|---------------|-----------|
| Low activity (incremental) | ~100-160 |
| Moderate activity (incremental) | ~50-80 |
| High activity (incremental) | ~12-25 |
| First import (moderate) | ~15-30 |

First imports fetch 21 days of events in a single run. Historical data fills in via progressive backfill — one 7-day chunk per run until reaching 90-day coverage (~10 runs, ~20 hours). Repos with pending backfill bypass the skip-unchanged check.

### Rate Limit Handling

The importer tracks remaining requests via `X-RateLimit-Remaining` headers. When remaining drops below 10, it sleeps until reset (with jitter). Maximum wait is capped at 15 minutes — longer waits return an error and the repo re-enters the queue.

Secondary (abuse) limits return HTTP 403 with `Retry-After`. The importer detects `AbuseRateLimitError` and retries after the specified wait (default: 60s).

### What scales linearly

- **GitHub API budget** — each tenant's installation token has its own 5,000/hr
- **Work distribution** — deterministic task-index sharding ensures no repo is processed twice

### What doesn't scale automatically

| Bottleneck | Symptom | Mitigation |
|------------|---------|------------|
| Serial processing | Job exceeds 1hr timeout | Increase `import_parallelism` |
| DB write contention | Slow event flushes, lock waits | Increase Cloud SQL tier, tune pool |
| Connection pool | `too many clients` errors | Increase pool size in DATABASE_URL |
| Cloud SQL CPU | High latency on upserts | Scale to `db-custom-*` tier |

## Scaling Vectors

Ordered by cost (cheapest first):

| Vector | Knob | Effect | Cost |
|--------|------|--------|------|
| Task parallelism | `import_parallelism` (Terraform) | More tasks = fewer repos per task = faster wall-clock | ~$0.01/task/run |
| Worker concurrency | `IMPORT_WORKERS` (env var) | More goroutines per task = more concurrent API calls | Free (limited by DB pool) |
| Schedule frequency | Cloud Scheduler cron | 2hr → 3hr → 4hr = more quota per run | Free |
| Token pool growth | GitHub App installations | Each tenant's installation adds 5000 calls/hr | Free (automatic with sign-ups) |
| Task timeout | `IMPORT_TASK_TIMEOUT` (env var) | Longer timeout = larger repos complete in single run | Free |
| DB tier | `db_tier` (Terraform) | More CPU/memory/connections for heavier query load | $$ |

**How weighted sharding scales:** The greedy bin-packing algorithm assigns the heaviest repos (by event count) to different tasks first. Adding tasks distributes large repos across more containers. With N large repos, set `import_parallelism >= N` so each large repo gets its own task.

**Token pool economics:** Every tenant who installs the GitHub App contributes 5000 API calls/hr to the shared pool. More customers = more quota = better service for everyone. Current pool: 8 tokens × 5000 = 40,000 calls/hr.

**Rate limit window:** GitHub rate limits reset on a 60-minute sliding window per installation token. A 2-hour import cycle guarantees full quota reset between runs. Extending to 3-4 hours provides even more headroom for large deployments.

## Database Scaling Path

Current: Cloud SQL `db-g1-small` (shared vCPU, 1.7GB RAM, ~$25-30/month).

| Trigger | Upgrade | Cost | Effort |
|---------|---------|------|--------|
| CPU > 80% sustained | `db-custom-2-4096` (dedicated 2 vCPU) | ~$50-80/month | Terraform variable change |
| Need analytical query speed | Managed AlloyDB | ~$100+/month | Connection string swap |
| Cost optimization at scale | AlloyDB Omni on GCE `e2-small` | ~$15-30/month | Self-managed VM |

**Why PostgreSQL stays:** RLS for tenant isolation (`SET config` per connection), ACID for session/tenant upserts, sub-10ms query latency for dashboard API. BigQuery's 1-3s minimum query time and lack of RLS make it unsuitable for the interactive dashboard workload.

**AlloyDB Omni option:** Google's PostgreSQL engine packaged for self-hosting. Free to run (pay only for compute). Same wire protocol — zero code changes, swap `DATABASE_URL`. Includes columnar engine for analytical queries (time-series aggregations, contributor rankings) without additional indexes. Trade-off: you manage backups, patching, and monitoring instead of Cloud SQL handling it.

**AlloyDB managed option:** Fully managed, same engine as Omni but with automated backups, patching, HA, and GCP Console integration. Higher cost but zero ops. Best fit when query performance matters more than cost.

**Upgrade is a connection string change:** All three options (Cloud SQL tier bump, AlloyDB managed, AlloyDB Omni) are PostgreSQL-compatible. The application connects via `DATABASE_URL` — no code changes, no schema migration, no driver swap needed.

## Database Indexes

Performance indexes beyond primary keys, defined in migration files:

**Event table** (`migrations/001_initial.sql`, `migrations/003_performance_indexes.sql`):

| Index | Columns | Purpose |
|-------|---------|---------|
| `idx_event_org_repo_date` | `(org, repo, date)` | Time-range queries on dashboard charts |
| `idx_event_org_repo_type_date` | `(org, repo, type, date)` | Event type distribution, filtered time series |
| `idx_event_org_repo_created_at` | `(org, repo, created_at)` | Timestamp-based queries (merge time, restore time) |
| `idx_event_username` | `(username)` | Developer lookup, profile queries |
| `idx_event_org_repo_number` | `(org, repo, number)` | PR/issue self-joins in insight queries |
| `idx_event_username_org_repo` | `(username, org, repo)` | RLS policy EXISTS joins |
| `idx_event_org_repo_number_type` | `(org, repo, number, type, created_at)` | Covering index for heaviest self-join queries (time-to-first-response, unanswered rate, review latency) |

**Developer table:**

| Index | Columns | Purpose |
|-------|---------|---------|
| `idx_developer_reputation` | `(reputation)` | Reputation ranking queries |
| `idx_developer_entity_null` | `(username) WHERE entity IS NULL` | Enrichment batch: find un-enriched developers |

**SaaS tables** (`migrations_saas/`):

| Index | Columns | Purpose |
|-------|---------|---------|
| `idx_session_tenant` | `(tenant_id)` | Session lookup by tenant |
| `idx_session_expires` | `(expires_at)` | Session expiry cleanup |
| `idx_tenant_repo_tenant` | `(tenant_id, active)` | Active repo listing per tenant |
| `idx_tenant_member_github` | `(github_id)` | OAuth login lookup |
| `idx_github_app_installation_tenant` | `(tenant_id)` | Installation token minting |
| `idx_tenant_repo_rls` | `(tenant_id, org, repo) WHERE active` | RLS policy performance |

## Terraform

All infrastructure is defined in `infra/saas/`:

| File | Resources |
|------|-----------|
| `providers.tf` | Terraform + Google provider config, GCS state backend (`thingzio-infra-state/devpulse`) |
| `variables.tf` | Project ID, region, domain, import parallelism, shared infra refs (VPC, subnet, DB) |
| `main.tf` | GCP API enablement, locals for shared infra references |
| `database.tf` | DB user (`devpulse`) in shared Cloud SQL instance |
| `secrets.tf` | Secret Manager secrets + IAM bindings |
| `iam.tf` | Service accounts (serve, import, deployer), WIF for GitHub Actions |
| `cloudrun.tf` | Cloud Run service (serve) + job (import) + service (admin, IAM-gated) |
| `scheduler.tf` | Import job trigger (every 2 hours) + daily report |
| `monitoring.tf` | Uptime checks, log-based metrics, service-level alert policies, email notifications |
| `registry.tf` | Artifact Registry standard repo (direct push from CI) |
| `outputs.tf` | Service URL, deployer SA, WIF provider |

Shared infrastructure (VPC, Cloud SQL instance, DB monitoring) is owned by the `thingzio/infra` repo.

## Cost Optimization

- **Serve service scale-to-zero** — `min_instance_count=0` eliminates always-on compute cost (~$10/mo savings); cold starts ~2s, within 1s p99 alert threshold
- **Response caching** — two-layer cache (5 min server + 30 min browser) eliminates ~95% of DB queries on the dashboard
- **AI gating by plan** — Free tenants generate zero Anthropic API cost
- **Insight caching** — 7-day age gate + 10% event delta gate reduces LLM calls by ~85%
- **Covering indexes** — heaviest self-join queries use index-only scans, 2-5x faster on cache miss
- **PK-sorted upserts** — all import batches sorted by primary key before execution, preventing deadlocks and reducing transaction retries
- **Import job exits after completion** — no idle compute; ~85% of hourly runs are no-ops (queue empty), exiting in <200ms
- **Admin service scale-to-zero** — no cost when not in use
- **Shared data model** — repos imported by one tenant are visible to others (no duplicate imports)
- **Incremental imports** — pagination state ensures only new data is fetched
- **Staleness checks** — metadata skipped if fresh (< 24h)
