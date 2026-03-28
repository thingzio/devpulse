# Monitoring

Observability for DevPulse across local development and GCP production.

## Local Development

### make stats

Quick snapshot of system state from the local Postgres:

```shell
make stats
```

Output:
```
Tenants              1
Active repos         3
Imported repos       2
Total events         807

Recent sign-ins:
  mchmarny             2026-03-26 21:02

Repos per tenant:
  mchmarny             3 repos
```

### Structured Logs

All logs are JSON (via `log/slog`). Enable debug level with `DEVPULSE_DEBUG=true`.

Key log messages to watch:

| Message | When | Fields |
|---------|------|--------|
| `starting` | Binary startup | version, commit, date |
| `server started` | HTTP server ready | address |
| `user signed in` | OAuth callback | username, tenant_id |
| `tos accepted` | ToS flow | tenant_id, username |
| `import worker starting` | Import begins | tenants |
| `importing repo` | Per-repo import | org, repo |
| `repo import complete` | Per-repo done | org, repo, errors, duration |
| `import worker complete` | Full run done | tenants, errors, duration |
| `tenant import failed` | Tenant error | tenant_id, username, error |

## GCP Production

### Log-Based Metrics

Created by Terraform (`infra/saas/monitoring.tf`). These are free — no additional cost.

| Metric | Filter | Type |
|--------|--------|------|
| `devpulse-saas-sign-ins` | `jsonPayload.msg="user signed in"` | Counter |
| `devpulse-saas-tos-accepted` | `jsonPayload.msg="tos accepted"` | Counter |
| `devpulse-saas-import-duration` | `jsonPayload.msg="import worker complete"` | Distribution |
| `devpulse-saas-import-tenant-count` | `jsonPayload.msg="import worker complete"` | Distribution |

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
| Import: Duration | Trending toward 45min = plan Cloud Tasks migration |

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
| Error rate | Cloud Run 5xx > 5/min for 5min | Investigate service logs |
| Import failure | Any failed import job execution | Check import logs |

### Scaling Signals

Use the dashboard to decide when to upgrade. See [INFRASTRUCTURE.md](INFRASTRUCTURE.md) for the full tier plan.

| Signal | Threshold | Action |
|--------|-----------|--------|
| Cloud SQL CPU | Sustained > 80% | Upgrade DB tier (`terraform apply -var="db_tier=..."`) |
| Cloud SQL connections | > 80 | Add PgBouncer sidecar |
| Import duration | > 45 min | Migrate to Cloud Tasks for parallel imports |
| Tenant count | > 100 | Evaluate db-g1-small |
| Tenant count | > 300 | Evaluate db-custom-1-3840 |
| Tenant count | > 1,000 | Evaluate AlloyDB |

### Querying Logs Directly

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
