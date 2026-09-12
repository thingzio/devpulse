# Administration

Day-2 operations for a running DevPulse deployment. For initial setup, see [BOOTSTRAP.md](BOOTSTRAP.md).

## Releases

```shell
make bump-patch  # or bump-minor, bump-major
```

Pushing a version tag triggers the CI release pipeline (test, build, push, deploy).

### Manual Deploy

```shell
gh workflow run deploy-saas.yaml -f image_tag=v1.2.3
```

### Infrastructure Changes

```shell
cd infra/run
terraform plan   # review changes
terraform apply  # zero downtime upgrade
```

For DB tier upgrades, change the `db_tier` variable in Terraform:

```hcl
variable "db_tier" {
  default = "db-g1-small"  # was "db-f1-micro"
}
```

See [INFRASTRUCTURE.md](INFRASTRUCTURE.md) for the full scaling plan and cost estimates per tier.

## Admin Dashboard

The admin dashboard is integrated into `devpulse-site` at `/admin`. Access requires session authentication and inclusion in the `DEVPULSE_ADMIN_USERS` whitelist (set via environment variable in Terraform).

Admin pages: tenant list, tenant detail, token quota, metrics overview.

## CLI Tools

Scripts in the `tools/` directory:

```shell
./tools/bump                       # version bump (used by make bump-*)
./tools/db-attach                  # attach to local/remote Postgres via Docker
./tools/job-detail <execution>     # show import job execution status and logs
./tools/setup-gh-env               # populate GitHub Actions environment from Terraform outputs
./tools/setup-tools                # install/validate local dev tool dependencies
./tools/e2e                        # end-to-end test runner
./tools/common                     # shared shell helpers (sourced by other scripts)
```

## Tenant Management

### Direct SQL

```sql
-- View tenants
SELECT username, plan, max_repos, max_events_per_week, created_at
FROM tenant ORDER BY created_at;

-- Promote to pro
UPDATE tenant
SET plan = 'pro', max_repos = 25, max_events_per_week = 15000, updated_at = NOW()
WHERE username = 'their-github-username';
```

Plan limits are defined in `pkg/plan/plan.go` (single source of truth):

| Plan | Repos | Events/Week | Data Retention | AI Level | Exports |
|------|-------|-------------|----------------|----------|---------|
| free | 1 | 500 | 3 months | none | none |
| starter | 5 | 2,500 | 6 months | basic | PDF |
| pro | 25 | 15,000 | 12 months | full | PDF+CSV |
| enterprise | unlimited | unlimited | unlimited | full | PDF+CSV |

> These names are stored values in the `tenant.plan` column, not product tiers.
> DevPulse has no plans and no pricing; every feature is available to every
> account. The limits exist only to keep a shared instance responsive, and the
> names are retained because renaming them means migrating live rows. See
> `pkg/plan/plan.go`.

## Monitoring

### Log-Based Metrics

Created by Terraform (`infra/run/monitoring.tf`). These are free.

| Metric | Filter | Type |
|--------|--------|------|
| `devpulse-saas-sign-ins` | `jsonPayload.msg="user signed in"` | Counter |
| `devpulse-saas-tos-accepted` | `jsonPayload.msg="tos accepted"` | Counter |
| `devpulse-saas-import-duration` | `jsonPayload.msg="import worker complete"` | Distribution |
| `devpulse-saas-event-limit-reached` | `jsonPayload.msg="weekly event limit reached"` | Counter (by tenant_id) |
| `devpulse-saas-import-repo-errors` | `jsonPayload.msg="imported" AND jsonPayload.errs>0` | Counter (by repo) |
| `devpulse-saas-import-repo-skipped-unchanged` | `jsonPayload.msg="skipping unchanged repo"` | Counter (by repo) |
| `devpulse-saas-backfill-rate-limited` | `jsonPayload.msg="backfill rate limited"` | Counter (by repo) |
| `devpulse-saas-backfill-completed` | `jsonPayload.msg="backfilled"` | Distribution (updated count) |
| `devpulse-saas-webhook-installs` | `jsonPayload.msg="installation event"` | Counter |
| `devpulse-saas-upgrade-requests` | `jsonPayload.msg="upgrade requested"` | Counter |

Metrics appear in Cloud Monitoring as `logging.googleapis.com/user/<metric_name>`.

### Dashboard

22 widgets in `infra/run/dashboard.json`:

#### Service Widgets (Cloud Run)

| Widget | What to watch |
|--------|--------------|
| Request Count | Traffic volume, 5xx spike = incident |
| Request Latency (p50/p95/p99) | p99 > 2s = investigate |
| Instance Count | Scaling behavior, stuck instances |
| CPU Utilization | Sustained > 80% = add CPU limit |
| Memory Utilization | Sustained > 80% = add memory limit |
| Billable Instance Time | Cost tracking |
| Application Errors | Error spikes across all services |

#### Import Widgets

| Widget | What to watch |
|--------|--------------|
| Import: Duration | Trending toward 45min = increase parallelism |
| Import: Job Execution Results | Failed vs succeeded executions |
| Import: Repo Errors | Persistent repo-level failures |

#### Tenant Widgets (log-based)

| Widget | What to watch |
|--------|--------------|
| Sign-ins | New tenant growth rate |
| ToS Accepted | Conversion from sign-in to active user |
| Webhook: Installation Events | GitHub App install/uninstall activity |
| Event Limit Reached | Tenants hitting weekly caps |

#### Database Widgets (Cloud SQL)

| Widget | What to watch |
|--------|--------------|
| CPU Utilization | Sustained > 80% = upgrade DB tier |
| Memory Utilization | Sustained > 80% = upgrade DB tier |
| Connections | Approaching max = add PgBouncer sidecar |
| Disk Used | Storage growth trend |
| Transactions/sec | Query load patterns |
| Deadlocks | Should be zero; investigate any occurrence |

### Alert Policies

| Alert | Condition | Action |
|-------|-----------|--------|
| Upgrade request | User requested plan upgrade | Review and process upgrade |
| Error rate | Cloud Run 5xx > 5/min for 5min | Investigate service logs |
| High latency | p99 latency > 1s for 600s | Check slow queries, DB load |
| DB CPU | Cloud SQL CPU sustained > 80% for 5min | Upgrade DB tier |
| DB connections | Connection count > 80 for 5min | Increase pool size or add PgBouncer |
| DB vacuum lag | Oldest transaction age > 200M for 5min | Check long-running queries, run VACUUM |
| Import failure | Any failed import job execution | Check import logs |
| Import repo errors | Repo import errors > 5/hour | Check import logs for persistent repo failures |

### Querying Logs

```shell
# Recent sign-ins
gcloud logging read \
    'resource.type="cloud_run_revision" jsonPayload.msg="user signed in"' \
    --project=$PROJECT_ID --limit=10 \
    --format='table(timestamp, jsonPayload.username)'

# Import job results
gcloud logging read \
    'resource.type="cloud_run_job" jsonPayload.msg="import worker complete"' \
    --project=$PROJECT_ID --limit=5 \
    --format='table(timestamp, jsonPayload.repos, jsonPayload.errors, jsonPayload.duration)'

# Errors in last hour
gcloud logging read \
    'resource.type="cloud_run_revision" severity="ERROR"' \
    --project=$PROJECT_ID --freshness=1h --limit=20 \
    --format='table(timestamp, jsonPayload.msg, jsonPayload.error)'

# Import execution details
gcloud run jobs executions list --job devpulse-saas-import \
    --project=$PROJECT_ID --region=us-west1 --limit=5
```

## Secret Rotation

1. Regenerate in GitHub (OAuth App settings / GitHub App settings)
2. Update in Secret Manager:
   ```shell
   echo -n "NEW_VALUE" | gcloud secrets versions add <secret-name> \
       --project=$PROJECT_ID --data-file=-
   ```
3. Redeploy: `make bump-patch`

## CI/CD

GitHub Actions workflows in `.github/workflows/`:

| Workflow | Trigger | Purpose |
|----------|---------|---------|
| `test-on-push.yaml` | push to main, PRs (paths-ignore: md, docs, .claude) | Calls reusable test workflow |
| `test-on-call.yaml` | reusable (workflow_call) | tidy, lint, test with race detector |
| `terraform-scan-on-push.yaml` | push to main, PRs (paths: infra/**) | Terraform security scanning, via `thingzio/actions` |
| `release-on-tag.yaml` | version tags (`v*.*.*`) | goreleaser build, image push, Cloud Run deploy |
| `deploy-cloud-run.yaml` | reusable (workflow_call) | Cloud Run deployment (called by release and deploy-saas) |
| `deploy-saas.yaml` | manual (workflow_dispatch) | Deploy devpulse to Cloud Run |

Supply chain: container images built via ko, pushed to Artifact Registry (`us-west1-docker.pkg.dev/thingzio/devpulse-saas-images`). govulncheck in CI. All GitHub Actions pinned by commit hash. Tool versions centralized in `.settings.yaml`.

## Related Documentation

- [BOOTSTRAP.md](BOOTSTRAP.md) — initial GCP deployment (one-time)
- [INFRASTRUCTURE.md](INFRASTRUCTURE.md) — scaling plan, costs, API throughput
- [DEVELOPMENT.md](DEVELOPMENT.md) — local dev, testing, debugging
- [ARCHITECTURE.md](ARCHITECTURE.md) — system design and data flow
