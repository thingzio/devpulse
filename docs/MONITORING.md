# Monitoring

GCP production observability for DevPulse. For local development monitoring, see [DEVELOPMENT.md](DEVELOPMENT.md).

## Log-Based Metrics

Created by Terraform (`infra/saas/monitoring.tf`). These are free — no additional cost.

| Metric | Filter | Type |
|--------|--------|------|
| `devpulse-saas-sign-ins` | `jsonPayload.msg="user signed in"` | Counter |
| `devpulse-saas-tos-accepted` | `jsonPayload.msg="tos accepted"` | Counter |
| `devpulse-saas-import-duration` | `jsonPayload.msg="import worker complete"` | Distribution |
| `devpulse-saas-import-tenant-count` | `jsonPayload.msg="import worker complete"` | Distribution |
| `devpulse-saas-tenant-weekly-events` | `jsonPayload.msg="tenant usage"` | Distribution (by tenant_id) |
| `devpulse-saas-event-limit-reached` | `jsonPayload.msg="weekly event limit reached"` | Counter (by tenant_id) |

Metrics appear in Cloud Monitoring as `logging.googleapis.com/user/<metric_name>`.

### Cloud Monitoring Dashboard

12 widgets in `infra/saas/dashboard.json`, deployed via:

```shell
gcloud monitoring dashboards create \
    --project=$PROJECT_ID \
    --config-from-file=infra/saas/dashboard.json
```

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
| Import: Duration | Trending toward 45min = increase parallelism or plan Cloud Tasks migration |

#### Database Widgets (Cloud SQL)

| Widget | What to watch |
|--------|--------------|
| CPU Utilization | Sustained > 80% = upgrade DB tier |
| Memory Utilization | Sustained > 80% = upgrade DB tier |
| Connections | Approaching max = add PgBouncer sidecar |

### Alert Policies

Created by Terraform (`infra/saas/monitoring.tf`):

| Alert | Condition | Action |
|-------|-----------|--------|
| Upgrade request | User requested plan upgrade | Review and process upgrade |
| Error rate | Cloud Run 5xx > 5/min for 5min | Investigate service logs |
| High latency | p99 latency > threshold | Check slow queries, DB load |
| DB CPU | Cloud SQL CPU sustained > 80% | Upgrade DB tier |
| DB connections | Connection count approaching max | Increase pool size or add PgBouncer |
| Import failure | Any failed import job execution | Check import logs |

For scaling signals and upgrade thresholds, see [INFRASTRUCTURE.md](INFRASTRUCTURE.md).

## Querying Logs Directly

```shell
# Recent sign-ins
gcloud logging read \
    'resource.type="cloud_run_revision" jsonPayload.msg="user signed in"' \
    --project=$PROJECT_ID \
    --limit=10 \
    --format='table(timestamp, jsonPayload.username)'

# Import job results
gcloud logging read \
    'resource.type="cloud_run_job" jsonPayload.msg="import worker complete"' \
    --project=$PROJECT_ID \
    --limit=5 \
    --format='table(timestamp, jsonPayload.tenants, jsonPayload.errors, jsonPayload.duration)'

# Errors in last hour
gcloud logging read \
    'resource.type="cloud_run_revision" severity="ERROR"' \
    --project=$PROJECT_ID \
    --freshness=1h \
    --limit=20 \
    --format='table(timestamp, jsonPayload.msg, jsonPayload.error)'
```
