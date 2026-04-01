# Infrastructure

DevPulse runs on GCP: Cloud Run for compute, Cloud SQL (PostgreSQL) for storage, Cloud Scheduler for periodic imports.

## Architecture

```
Internet
   │
   ├── Cloud Run service (serve mode)
   │     ├── OAuth sign-in
   │     ├── Dashboard (Chart.js)
   │     ├── Webhook endpoint (GitHub App)
   │     └── Data API (30+ chart endpoints)
   │
   ├── Cloud Run job (import mode, hourly)
   │     └── Per-tenant repo import via GitHub API
   │
   ├── Cloud Run service (admin, IAM-gated)
   │     └── Tenant plan management (upgrade/downgrade)
   │
   └── GitHub
         ├── OAuth (user identity)
         ├── App webhooks (installation events)
         └── API (events, metadata, releases)

Cloud SQL PostgreSQL
   ├── Base tables (event, developer, repo_meta, release, ...)
   ├── SaaS tables (tenant, session, tenant_repo, ...)
   └── RLS policies (tenant isolation)

Cloud Scheduler → triggers import job hourly
Secret Manager  → GitHub App key, OAuth secret, webhook secret, Anthropic API key
Cloud DNS       → devpulse.thingz.io
```

## Compute

Three container images: `devpulse-site` (Cloud Run service), `devpulse-import` (Cloud Run job), `devpulse-admin` (Cloud Run service, IAM-protected).

| Mode | Deployment | Scaling | Access |
|------|-----------|---------|--------|
| Serve | Cloud Run service | 0-10 instances, scale-to-zero | Public |
| Import | Cloud Run job | Hourly, parallelism=3 (SKIP LOCKED queue) | Internal |
| Admin | Cloud Run service | 0-1 instances, scale-to-zero | IAM-gated |

Cloud Run scales to zero when idle — no cost when no one is using the dashboard. The import job runs for the duration of the import and exits. The admin service is IAM-protected (`roles/run.invoker`) for tenant management operations.

## Database Scaling Plan

Start small, upgrade in-place as tenant count grows. Each Cloud SQL tier change is a Terraform apply with zero downtime (with HA) or ~1-3 minutes (without). The AlloyDB migration is a planned maintenance event.

### Tier 1: Minimal (0-100 tenants)

**Cloud SQL db-f1-micro** — shared vCPU, 614MB RAM.

| Service | Details | Estimate |
|---------|---------|----------|
| Cloud SQL | db-f1-micro, 10GB storage | $9/mo |
| Cloud Run Service | scale-to-zero, 1 vCPU/512MB | $1-2/mo |
| Cloud Run Job | hourly, ~1 min/run | $0.50/mo |
| Anthropic API | Claude Haiku insights, per-repo/per-import | $1-5/mo |
| Cloud Scheduler | 1 hourly job | free (3 free) |
| Secret Manager | 5 secrets, ~2K accesses/mo | free tier |
| Artifact Registry | remote repo (GHCR proxy), <1GB | $0.10/mo |
| Cloud DNS | 1 hosted zone | $0.20/mo |
| Cloud Monitoring | log-based metrics, 6 alert policies, email | free tier |
| **Total** | | **~$12-17/mo** |

Handles low traffic dashboards and hourly imports for up to ~100 tenants. Import job completes in under 15 minutes. Anthropic cost scales with repo count and import frequency.

**Upgrade signal:** DB CPU sustained > 80%, or import duration > 30 minutes.

### Tier 2: Small (100-300 tenants)

**Cloud SQL db-g1-small** — shared vCPU, 1.7GB RAM.

| Service | Estimate |
|---------|----------|
| Cloud SQL | $25/mo |
| Storage (25GB) | $4/mo |
| Cloud Run | $15/mo |
| Anthropic API | $5-15/mo |
| Fixed (scheduler, DNS, secrets) | $1/mo |
| **Total** | **~$50-60/mo** |

Migration: `terraform apply` (change `db_tier` variable). Zero downtime with HA enabled.

**Upgrade signal:** connection count approaching limits, query latency > 500ms on dashboard.

### Tier 3: Dedicated (300-1,000 tenants)

**Cloud SQL db-custom-1-3840** — 1 dedicated vCPU, 3.75GB RAM.

| Service | Estimate |
|---------|----------|
| Cloud SQL | $50/mo |
| Storage (50GB) | $9/mo |
| Cloud Run | $30/mo |
| PgBouncer sidecar | $10/mo |
| Anthropic API | $15-40/mo |
| **Total** | **~$115-140/mo** |

Adds PgBouncer as a Cloud Run sidecar for connection pooling. Import parallelism can be increased to 5–10 via `import_parallelism` variable.

Migration: `terraform apply`. Zero downtime.

**Upgrade signal:** import job approaching 45-minute duration, analytics queries slow under concurrent load.

### Tier 4: Production (1,000+ tenants)

**AlloyDB** — 2+ vCPUs, built-in connection pooling, columnar analytics engine.

| Service | Estimate |
|---------|----------|
| AlloyDB primary (2 vCPU) | $150/mo |
| Storage (100GB) | $35/mo |
| Read pool (optional, 2 vCPU) | $150/mo |
| Cloud Run | $50/mo |
| Cloud Tasks (parallel import) | $10/mo |
| Anthropic API | $40-100/mo |
| **Total** | **~$300-500/mo** |

Why AlloyDB at this tier:
- **Connection pooling built-in** — no PgBouncer sidecar needed
- **Columnar engine** — dashboard aggregation queries (GROUP BY month, contributor counts) run 5-10x faster
- **Read pool** — dashboard reads separated from import writes, shared storage (no replication lag)

Migration: `pg_dump`/`pg_restore` (minutes of downtime) or Database Migration Service (seconds). No code changes — same PostgreSQL wire protocol, same `lib/pq` driver. Update `DATABASE_URL` and swap Cloud SQL Auth Proxy for AlloyDB Auth Proxy in Terraform.

## Import Worker Scaling

The import job uses a PostgreSQL SKIP LOCKED claim queue. Each Cloud Run task claims individual repos from the queue — multiple tasks safely share work without overlap. Current setting: `parallelism=3`.

| Tenants | ~Repos | `import_parallelism` | Est. Duration | Strategy |
|---------|--------|---------------------|--------------|----------|
| 1–10 | 5–100 | 3 (current) | < 15 min | Current settings |
| 10–50 | 50–500 | 3–5 | 15–30 min | Bump parallelism, increase DB pool |
| 50–200 | 250–2000 | 5–10 | 30–50 min | Scale DB tier + pool |
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

Each repo runs 7 import phases:

| Phase | Incremental | First import | Notes |
|-------|------------:|-------------:|-------|
| Metadata | 2 | 2 | `Repositories.Get` + `GetCommunityHealthMetrics` |
| Events (5 concurrent) | 10–30 | 50–200+ | PRs, reviews, issues, comments, forks (100/page) |
| PR size backfill | 0–20 | 50–200 | `PullRequests.Get` per new PR |
| Releases | 1–3 | 1–5 | `ListReleases` paginated |
| Metric history | 3–10 | 5–15 | Stars + forks pagination |
| Containers | 0–5 | 2–10 | Packages + versions |
| Reputation | 5–10/dev | 5–10/dev | User profile + org membership + search |

Typical totals: **~60–100 calls/repo** incremental, **~150–300** first import. The free tier (3 repos) and pro tier (15 repos) are well within the 5,000/hr budget. Enterprise is unlimited.

### Throughput Per Tenant

| Repo activity | Repos/hour |
|---------------|-----------|
| Low activity (incremental) | ~100–160 |
| Moderate activity (incremental) | ~50–80 |
| High activity (incremental) | ~12–25 |
| First import (moderate) | ~15–30 |

First imports of large repos may span multiple hourly cycles — the importer resumes from saved page state automatically.

### Rate Limit Handling

The importer tracks remaining requests via `X-RateLimit-Remaining` headers. When remaining drops below 10, it sleeps until reset (with jitter). Maximum wait is capped at 15 minutes — longer waits return an error and the repo re-enters the queue.

Secondary (abuse) limits return HTTP 403 with `Retry-After`. The importer detects `AbuseRateLimitError` and retries after the specified wait (default: 60s).

### What scales linearly

- **GitHub API budget** — each tenant's installation token has its own 5,000/hr
- **Queue work distribution** — SKIP LOCKED ensures no repo is claimed twice

### What doesn't scale automatically

| Bottleneck | Symptom | Mitigation |
|------------|---------|------------|
| Serial processing | Job exceeds 1hr timeout | Increase `import_parallelism` |
| DB write contention | Slow event flushes, lock waits | Increase Cloud SQL tier, tune pool |
| Connection pool | `too many clients` errors | Increase pool size in DATABASE_URL |
| Cloud SQL CPU | High latency on upserts | Scale to `db-custom-*` tier |

## Terraform

All infrastructure is defined in `infra/saas/`:

| File | Resources |
|------|-----------|
| `providers.tf` | Terraform + Google provider config, GCS state backend |
| `variables.tf` | Project ID, region, domain, DB tier, import parallelism |
| `main.tf` | GCP API enablement |
| `network.tf` | VPC, private service access |
| `database.tf` | Cloud SQL instance, database, IAM users |
| `secrets.tf` | Secret Manager secrets + IAM bindings |
| `iam.tf` | Service accounts (serve, import, deployer), WIF for GitHub Actions |
| `cloudrun.tf` | Cloud Run service (serve) + job (import) + service (admin, IAM-gated) |
| `scheduler.tf` | Hourly import trigger |
| `dns.tf` | Cloud DNS zone |
| `monitoring.tf` | Uptime checks, log-based metrics, alert policies, email notifications |
| `registry.tf` | Artifact Registry remote repo (GHCR proxy) |
| `outputs.tf` | Service URL, DB connection, DNS nameservers |

### Tier upgrades via Terraform

```hcl
# Tier 1 → Tier 2: change one variable
variable "db_tier" {
  default = "db-g1-small"  # was "db-f1-micro"
}
```

```shell
terraform plan   # review changes
terraform apply  # zero downtime upgrade
```

## Cost Optimization

- **Cloud Run scale-to-zero** — no cost when no dashboard users are active
- **Import job exits after completion** — no idle compute
- **Shared data model** — repos imported by one tenant are visible to others (no duplicate imports)
- **Incremental imports** — pagination state ensures only new data is fetched
- **Staleness checks** — metadata skipped if fresh (< 24h)
