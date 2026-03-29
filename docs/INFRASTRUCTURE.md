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
   └── GitHub
         ├── OAuth (user identity)
         ├── App webhooks (installation events)
         └── API (events, metadata, releases)

Cloud SQL PostgreSQL
   ├── Base tables (event, developer, repo_meta, release, ...)
   ├── SaaS tables (tenant, session, tenant_repo, ...)
   └── RLS policies (tenant isolation)

Cloud Scheduler → triggers import job hourly
Secret Manager  → GitHub App key, OAuth secret, webhook secret
Cloud DNS       → devpulse.thingz.io
```

## Compute

Two container images: `ghcr.io/thingzio/devpulse-site` (Cloud Run service) and `ghcr.io/thingzio/devpulse-import` (Cloud Run job).

| Mode | Deployment | Scaling |
|------|-----------|---------|
| Serve | Cloud Run service | 0-10 instances, scale-to-zero |
| Import | Cloud Run job | Single execution, hourly |

Cloud Run scales to zero when idle — no cost when no one is using the dashboard. The import job runs for the duration of the import and exits.

## Database Scaling Plan

Start small, upgrade in-place as tenant count grows. Each Cloud SQL tier change is a Terraform apply with zero downtime (with HA) or ~1-3 minutes (without). The AlloyDB migration is a planned maintenance event.

### Tier 1: Minimal (0-100 tenants)

**Cloud SQL db-f1-micro** — shared vCPU, 614MB RAM.

| | Estimate |
|---|---|
| Cloud SQL | $7/mo |
| Storage (10GB) | $2/mo |
| Cloud Run (serve + import) | $5/mo |
| Fixed (scheduler, secrets, DNS) | $1/mo |
| **Total** | **~$15/mo** |

Handles low traffic dashboards and hourly imports for up to ~100 tenants. Import job completes in under 15 minutes.

**Upgrade signal:** DB CPU sustained > 80%, or import duration > 30 minutes.

### Tier 2: Small (100-300 tenants)

**Cloud SQL db-g1-small** — shared vCPU, 1.7GB RAM.

| | Estimate |
|---|---|
| Cloud SQL | $25/mo |
| Storage (25GB) | $4/mo |
| Cloud Run | $15/mo |
| **Total** | **~$45/mo** |

Migration: `terraform apply` (change `db_tier` variable). Zero downtime with HA enabled.

**Upgrade signal:** connection count approaching limits, query latency > 500ms on dashboard.

### Tier 3: Dedicated (300-1,000 tenants)

**Cloud SQL db-custom-1-3840** — 1 dedicated vCPU, 3.75GB RAM.

| | Estimate |
|---|---|
| Cloud SQL | $50/mo |
| Storage (50GB) | $9/mo |
| Cloud Run | $30/mo |
| PgBouncer sidecar | $10/mo |
| **Total** | **~$100/mo** |

Adds PgBouncer as a Cloud Run sidecar for connection pooling. Import job may need to split to Cloud Tasks for parallel per-tenant processing (1hr timeout at ~300 tenants sequential).

Migration: `terraform apply`. Zero downtime.

**Upgrade signal:** import job approaching 45-minute duration, analytics queries slow under concurrent load.

### Tier 4: Production (1,000+ tenants)

**AlloyDB** — 2+ vCPUs, built-in connection pooling, columnar analytics engine.

| | Estimate |
|---|---|
| AlloyDB primary (2 vCPU) | $150/mo |
| Storage (100GB) | $35/mo |
| Read pool (optional, 2 vCPU) | $150/mo |
| Cloud Run | $50/mo |
| Cloud Tasks (parallel import) | $10/mo |
| **Total** | **~$250-400/mo** |

Why AlloyDB at this tier:
- **Connection pooling built-in** — no PgBouncer sidecar needed
- **Columnar engine** — dashboard aggregation queries (GROUP BY month, contributor counts) run 5-10x faster
- **Read pool** — dashboard reads separated from import writes, shared storage (no replication lag)

Migration: `pg_dump`/`pg_restore` (minutes of downtime) or Database Migration Service (seconds). No code changes — same PostgreSQL wire protocol, same `lib/pq` driver. Update `DATABASE_URL` and swap Cloud SQL Auth Proxy for AlloyDB Auth Proxy in Terraform.

## Import Worker Scaling

The import job processes tenants sequentially. As tenant count grows:

| Tenants | ~Repos | Est. Duration | Strategy |
|---------|--------|--------------|----------|
| 50 | 250 | ~12 min | Single job (current) |
| 200 | 1,000 | ~50 min | Single job (approaching limit) |
| 300+ | 1,500+ | >1 hr | **Migrate to Cloud Tasks** |

Cloud Tasks migration path (designed in from day one):
1. Cloud Scheduler triggers a dispatcher endpoint
2. Dispatcher queries active tenants and enqueues one Cloud Task per tenant
3. Each task calls the same `importTenant` function
4. Parallel execution, no timeout concern

The `importTenant` function in `pkg/importer/importer.go` is already the unit of work — only the dispatch layer changes.

## Terraform

All infrastructure is defined in `infra/saas/`:

| File | Resources |
|------|-----------|
| `providers.tf` | Terraform + Google provider config, GCS state backend |
| `variables.tf` | Project ID, region, domain, DB tier |
| `main.tf` | GCP API enablement |
| `network.tf` | VPC, private service access |
| `database.tf` | Cloud SQL instance, database, IAM users |
| `secrets.tf` | Secret Manager secrets + IAM bindings |
| `iam.tf` | Service accounts (serve, import, deployer), WIF for GitHub Actions |
| `cloudrun.tf` | Cloud Run service + job |
| `scheduler.tf` | Hourly import trigger |
| `dns.tf` | Cloud DNS zone |
| `monitoring.tf` | Uptime checks, error rate + import failure alerts |
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
