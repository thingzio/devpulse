# Upgrade Request Feature

## Problem

When users hit the repo limit, they see a raw error message with no way to request an upgrade. The admin has no visibility into upgrade demand.

## Design

Idempotent upgrade request with log-based alerting and dashboard metrics.

### Schema

Add `upgrade_requested_at TIMESTAMPTZ` (nullable, default NULL) to `tenant` table.

### Flow

```
User hits repo limit → sees "Repo limit reached (5). [Request Upgrade]"
  → POST /api/upgrade-request
  → UPDATE tenant SET upgrade_requested_at = NOW() WHERE id = $1 AND upgrade_requested_at IS NULL
  → If first request: log with tenant details → metric fires → alert emails admin
  → Always returns 200 {"status": "ok"}
  → Button changes to "Request sent"
```

### Backend Changes

1. **Migration** — `ALTER TABLE tenant ADD COLUMN upgrade_requested_at TIMESTAMPTZ`
2. **`pkg/tenant/tenant.go`** — add `UpgradeRequestedAt` field, update all SQL constants and `scanTenant()`
3. **`pkg/server/server.go`** — new `POST /api/upgrade-request` handler; update `addRepoHandler` to return structured limit error
4. **`infra/saas/monitoring.tf`** — add `upgrade-requests` log-based metric and alert policy
5. **`infra/saas/dashboard.json`** — add "Upgrade Requests" widget

### Frontend Changes

6. **`pkg/server/static/js/app.js`** — detect repo limit error, show message with "Request Upgrade" button; button POSTs and changes to "Request sent"

### Error Message

Old: `repo limit exceeded for plan: 5 + 1 > 5`
New: `repo_limit_reached:5` (parsed by frontend into user-friendly message with upgrade button)

### Admin Workflow

Clear request when upgrading:
```sql
UPDATE tenant SET plan = 'pro', max_repos = 25, max_events_per_week = 20000,
  upgrade_requested_at = NULL, updated_at = NOW()
WHERE username = 'their-username';
```

### Idempotency

The `WHERE upgrade_requested_at IS NULL` clause makes the update a no-op on subsequent requests. The log/metric only fires on the first request. The user always sees success.
