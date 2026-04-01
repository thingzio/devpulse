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
cd infra/saas
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

## Tenant Management

### CLI Tools

```shell
./tools/tenant-list                          # list all tenants
./tools/tenant-upgrade <username> <plan>     # upgrade tenant plan (free/pro/enterprise)
```

Both tools call the IAM-protected `devpulse-saas-admin` Cloud Run service.

### Direct SQL

```sql
-- View tenants
SELECT username, plan, max_repos, max_events_per_week, created_at
FROM tenant ORDER BY created_at;

-- Promote to pro
UPDATE tenant
SET plan = 'pro', max_repos = 15, max_events_per_week = 15000, updated_at = NOW()
WHERE username = 'their-github-username';
```

Plan limits are defined in `pkg/plan/plan.go` (single source of truth):

| Plan | Repos | Events/Week |
|------|-------|-------------|
| free | 3 | 1,000 |
| pro | 15 | 15,000 |
| enterprise | Unlimited | Unlimited |

## Monitoring

### Log-Based Metrics

Created by Terraform (`infra/saas/monitoring.tf`). These are free.

| Metric | Filter | Type |
|--------|--------|------|
| `devpulse-saas-sign-ins` | `jsonPayload.msg="user signed in"` | Counter |
| `devpulse-saas-tos-accepted` | `jsonPayload.msg="tos accepted"` | Counter |
| `devpulse-saas-import-duration` | `jsonPayload.msg="import worker complete"` | Distribution |
| `devpulse-saas-import-tenant-count` | `jsonPayload.msg="import worker complete"` | Distribution |
| `devpulse-saas-tenant-weekly-events` | `jsonPayload.msg="tenant usage"` | Distribution (by tenant_id) |
| `devpulse-saas-event-limit-reached` | `jsonPayload.msg="weekly event limit reached"` | Counter (by tenant_id) |

Metrics appear in Cloud Monitoring as `logging.googleapis.com/user/<metric_name>`.

### Dashboard

12 widgets in `infra/saas/dashboard.json`:

#### Service Widgets (Cloud Run)

| Widget | What to watch |
|--------|--------------|
| Request Count | Traffic volume, 5xx spike = incident |
| Request Latency (p50/p95/p99) | p99 > 2s = investigate |
| Instance Count | Scaling behavior, stuck instances |
| CPU Utilization | Sustained > 80% = add CPU limit |
| Memory Utilization | Sustained > 80% = add memory limit |
| Billable Instance Time | Cost tracking |

#### Tenant Widgets (log-based)

| Widget | What to watch |
|--------|--------------|
| Sign-ins | New tenant growth rate |
| Import: Tenant Count | Active tenants per import run |
| Import: Duration | Trending toward 45min = increase parallelism |

#### Database Widgets (Cloud SQL)

| Widget | What to watch |
|--------|--------------|
| CPU Utilization | Sustained > 80% = upgrade DB tier |
| Memory Utilization | Sustained > 80% = upgrade DB tier |
| Connections | Approaching max = add PgBouncer sidecar |

### Alert Policies

| Alert | Condition | Action |
|-------|-----------|--------|
| Upgrade request | User requested plan upgrade | Review and process upgrade |
| Error rate | Cloud Run 5xx > 5/min for 5min | Investigate service logs |
| High latency | p99 latency > threshold | Check slow queries, DB load |
| DB CPU | Cloud SQL CPU sustained > 80% | Upgrade DB tier |
| DB connections | Connection count approaching max | Increase pool size or add PgBouncer |
| Import failure | Any failed import job execution | Check import logs |

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
    --format='table(timestamp, jsonPayload.tenants, jsonPayload.errors, jsonPayload.duration)'

# Errors in last hour
gcloud logging read \
    'resource.type="cloud_run_revision" severity="ERROR"' \
    --project=$PROJECT_ID --freshness=1h --limit=20 \
    --format='table(timestamp, jsonPayload.msg, jsonPayload.error)'

# Import execution details
gcloud run jobs executions list --job devpulse-saas-import \
    --project=$PROJECT_ID --region=us-west1 --limit=5
```

### Local Stats

```shell
make stats
```

Shows tenant count, active repos, imported repos, total events, recent sign-ins, and repos per tenant.

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
| `test-on-push.yaml` | push to main, PRs | Calls reusable test workflow |
| `test-on-call.yaml` | reusable (workflow_call) | tidy, lint, test with race detector |
| `release-on-tag.yaml` | version tags (`v*.*.*`) | goreleaser build, image push, Cloud Run deploy |
| `deploy-saas.yaml` | manual (workflow_dispatch) | Deploy devpulse to Cloud Run |

Supply chain: container images built via ko, pushed to GHCR (`ghcr.io/thingzio/devpulse`). govulncheck in CI. All GitHub Actions pinned by commit hash. Tool versions centralized in `.settings.yaml`.

## Related Documentation

- [BOOTSTRAP.md](BOOTSTRAP.md) — initial GCP deployment (one-time)
- [INFRASTRUCTURE.md](INFRASTRUCTURE.md) — scaling plan, costs, API throughput
- [DEVELOPMENT.md](DEVELOPMENT.md) — local dev, testing, debugging
- [ARCHITECTURE.md](ARCHITECTURE.md) — system design and data flow
