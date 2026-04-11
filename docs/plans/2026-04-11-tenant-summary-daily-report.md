# Tenant Summary & Daily Report Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add platform-level business metrics (tenant/repo/event counts with DoD/WoW/MoM deltas), a daily email report combining business + infra metrics, and a CLI tool for on-demand summary.

**Architecture:** New `platform_stats` table stores daily snapshots. `GET /summary` upserts today's snapshot and returns structured JSON with historical deltas. `POST /report` orchestrates summary + metrics analysis and sends HTML email via SendGrid. `GET /metrics/review` renamed to `GET /metrics`.

**Tech Stack:** Go, PostgreSQL, SendGrid v3 REST API (no SDK), GCP Secret Manager, Cloud Scheduler

---

### Task 1: Database Migration — `platform_stats` Table

**Files:**
- Create: `pkg/data/postgres/sql/migrations_saas/010_platform_stats.sql`

**Step 1: Write migration SQL**

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

**Step 2: Verify migration numbering**

Existing migrations go up to `009_tenant_profile_columns.sql`. Next is `010`.

**Step 3: Run tests to verify migration applies**

Run: `make test`
Expected: PASS (migrations auto-apply in test containers)

**Step 4: Commit**

```bash
git add pkg/data/postgres/sql/migrations_saas/010_platform_stats.sql
git commit -S -m "feat: add platform_stats table for daily snapshots"
```

---

### Task 2: Summary Types — Response Structs

**Files:**
- Modify: `pkg/admin/types.go`

**Step 1: Add response types**

Append to `pkg/admin/types.go`:

```go
type platformStats struct {
	Tenants           int   `json:"tenants"`
	TenantsFree       int   `json:"tenants_free"`
	TenantsStarter    int   `json:"tenants_starter"`
	TenantsPro        int   `json:"tenants_pro"`
	TenantsEnterprise int   `json:"tenants_enterprise"`
	Repos             int   `json:"repos"`
	Events            int64 `json:"events"`
	Contributors      int   `json:"contributors"`
	Installations     int   `json:"installations"`
	ReposWithErrors   int   `json:"repos_with_errors"`
}

type statsDelta struct {
	Tenants        *int     `json:"tenants,omitempty"`
	TenantsPct     *float64 `json:"tenants_pct,omitempty"`
	Repos          *int     `json:"repos,omitempty"`
	ReposPct       *float64 `json:"repos_pct,omitempty"`
	Events         *int64   `json:"events,omitempty"`
	EventsPct      *float64 `json:"events_pct,omitempty"`
	Contributors   *int     `json:"contributors,omitempty"`
	ContribPct     *float64 `json:"contributors_pct,omitempty"`
	Installations  *int     `json:"installations,omitempty"`
	InstallPct     *float64 `json:"installations_pct,omitempty"`
	ReposWithErrors *int    `json:"repos_with_errors,omitempty"`
	ErrorsPct      *float64 `json:"repos_with_errors_pct,omitempty"`
}

type errorRepo struct {
	Org       string `json:"org"`
	Repo      string `json:"repo"`
	Errors    int    `json:"errors"`
	LastError string `json:"last_error"`
}

type summaryResponse struct {
	Date      string        `json:"date"`
	Current   platformStats `json:"current"`
	DoD       *statsDelta   `json:"dod,omitempty"`
	WoW       *statsDelta   `json:"wow,omitempty"`
	MoM       *statsDelta   `json:"mom,omitempty"`
	ErrorRepos []errorRepo  `json:"error_repos"`
	UpdatedAt string        `json:"updated_at"`
}

type reportResponse struct {
	Sent bool   `json:"sent"`
	Error string `json:"error,omitempty"`
}

type reportConfig struct {
	SendGridAPIKey string `json:"sendgrid_api_key"`
	ToEmail        string `json:"to_email"`
	FromEmail      string `json:"from_email"`
}
```

**Step 2: Commit**

```bash
git add pkg/admin/types.go
git commit -S -m "feat: add summary and report response types"
```

---

### Task 3: Summary Handler — Snapshot Upsert & Delta Computation

**Files:**
- Create: `pkg/admin/summary.go`
- Create: `pkg/admin/summary_test.go`

**Step 1: Write tests for delta computation**

Create `pkg/admin/summary_test.go` with tests for `computeDelta`:

```go
package admin

import "testing"

func TestComputeDelta(t *testing.T) {
	tests := []struct {
		name    string
		current platformStats
		prev    *platformStats
		wantNil bool
	}{
		{
			name:    "nil previous returns nil delta",
			current: platformStats{Tenants: 10},
			prev:    nil,
			wantNil: true,
		},
		{
			name:    "computes absolute and percentage deltas",
			current: platformStats{Tenants: 12, Repos: 100, Events: 5000},
			prev:    &platformStats{Tenants: 10, Repos: 90, Events: 4000},
			wantNil: false,
		},
		{
			name:    "zero previous avoids divide by zero",
			current: platformStats{Tenants: 5},
			prev:    &platformStats{Tenants: 0},
			wantNil: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := computeDelta(tt.current, tt.prev)
			if tt.wantNil {
				if got != nil {
					t.Errorf("expected nil delta, got %+v", got)
				}
				return
			}
			if got == nil {
				t.Fatal("expected non-nil delta")
			}
			if tt.name == "computes absolute and percentage deltas" {
				if got.Tenants == nil || *got.Tenants != 2 {
					t.Errorf("tenants delta: want 2, got %v", got.Tenants)
				}
				if got.TenantsPct == nil || *got.TenantsPct != 20.0 {
					t.Errorf("tenants pct: want 20.0, got %v", got.TenantsPct)
				}
			}
		})
	}
}
```

**Step 2: Run test to verify it fails**

Run: `make test`
Expected: FAIL — `computeDelta` undefined

**Step 3: Write `pkg/admin/summary.go`**

This file contains:
- SQL constants for aggregate counts and snapshot upsert/read
- `computeDelta(current, prev)` pure function
- `handleSummary(db)` handler

SQL queries needed:

1. **Aggregate current counts** (run as single query for consistency):
```sql
SELECT
    (SELECT COUNT(*) FROM tenant) AS tenants,
    (SELECT COUNT(*) FROM tenant WHERE plan = 'free') AS tenants_free,
    (SELECT COUNT(*) FROM tenant WHERE plan = 'starter') AS tenants_starter,
    (SELECT COUNT(*) FROM tenant WHERE plan = 'pro') AS tenants_pro,
    (SELECT COUNT(*) FROM tenant WHERE plan = 'enterprise') AS tenants_enterprise,
    (SELECT COUNT(*) FROM tenant_repo WHERE active = TRUE) AS repos,
    (SELECT COUNT(*) FROM event) AS events,
    (SELECT COUNT(DISTINCT username) FROM developer
     WHERE username NOT LIKE '%[bot]') AS contributors,
    (SELECT COUNT(*) FROM github_app_installation
     WHERE suspended_at IS NULL) AS installations,
    (SELECT COUNT(DISTINCT id) FROM tenant_repo
     WHERE active = TRUE AND import_errors > 0) AS repos_with_errors
```

2. **Upsert snapshot**:
```sql
INSERT INTO platform_stats (date, tenants, tenants_free, tenants_starter,
    tenants_pro, tenants_enterprise, repos, events, contributors,
    installations, repos_with_errors, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, NOW())
ON CONFLICT (date) DO UPDATE SET
    tenants = EXCLUDED.tenants,
    tenants_free = EXCLUDED.tenants_free,
    tenants_starter = EXCLUDED.tenants_starter,
    tenants_pro = EXCLUDED.tenants_pro,
    tenants_enterprise = EXCLUDED.tenants_enterprise,
    repos = EXCLUDED.repos,
    events = EXCLUDED.events,
    contributors = EXCLUDED.contributors,
    installations = EXCLUDED.installations,
    repos_with_errors = EXCLUDED.repos_with_errors,
    updated_at = NOW()
```

3. **Read snapshot by date**:
```sql
SELECT tenants, tenants_free, tenants_starter, tenants_pro, tenants_enterprise,
    repos, events, contributors, installations, repos_with_errors
FROM platform_stats WHERE date = $1
```

4. **Error repos**:
```sql
SELECT org, repo, import_errors, COALESCE(import_last_error, '')
FROM tenant_repo
WHERE active = TRUE AND import_errors > 0
ORDER BY import_errors DESC
```

Handler flow:
1. Query aggregate counts → `platformStats`
2. Upsert into `platform_stats` for today
3. Read rows for yesterday (DoD), 7 days ago (WoW), 30 days ago (MoM)
4. `computeDelta()` for each comparison
5. Query error repos
6. Return `summaryResponse` JSON

**Step 4: Run tests**

Run: `make test`
Expected: PASS

**Step 5: Commit**

```bash
git add pkg/admin/summary.go pkg/admin/summary_test.go
git commit -S -m "feat: add GET /summary endpoint with platform stats and deltas"
```

---

### Task 4: Rename Metrics Endpoint

**Files:**
- Modify: `pkg/admin/admin.go` (change route from `/metrics/review` to `/metrics`)
- Modify: `tools/metrics-review` (change URL path)

**Step 1: Update route in `admin.go`**

Change line 54:
```go
// Before:
mux.HandleFunc("GET /metrics/review", handleMetricsReview(mcfg))
// After:
mux.HandleFunc("GET /metrics", handleMetricsReview(mcfg))
```

**Step 2: Update `tools/metrics-review` script**

Change the curl URL from `${ADMIN_URL}/metrics/review?days=${DAYS}` to `${ADMIN_URL}/metrics?days=${DAYS}`.

**Step 3: Run tests**

Run: `make test`
Expected: PASS

**Step 4: Commit**

```bash
git add pkg/admin/admin.go tools/metrics-review
git commit -S -m "refactor: rename /metrics/review to /metrics"
```

---

### Task 5: Wire Summary Route Into Admin Server

**Files:**
- Modify: `pkg/admin/admin.go` (add route, pass `db` to handler)

**Step 1: Add summary route**

In `Run()`, after existing routes, add:
```go
mux.HandleFunc("GET /summary", handleSummary(db))
```

**Step 2: Run tests**

Run: `make test`
Expected: PASS

**Step 3: Commit**

```bash
git add pkg/admin/admin.go
git commit -S -m "feat: wire GET /summary route in admin server"
```

---

### Task 6: Report Handler — Email Composition & SendGrid

**Files:**
- Create: `pkg/admin/report.go`
- Create: `pkg/admin/report_test.go`

**Step 1: Write tests for email HTML rendering**

Create `pkg/admin/report_test.go`:
- Test `renderReportHTML(summary, analysis)` produces valid HTML with summary table and analysis section
- Test `loadReportConfig()` returns error when secret is missing
- Test `loadReportConfig()` parses valid JSON

**Step 2: Run test to verify it fails**

Run: `make test`
Expected: FAIL — functions undefined

**Step 3: Write `pkg/admin/report.go`**

Contents:
- `loadReportConfig()` — reads `REPORT_CONFIG` env var (JSON string from secret mount) into `reportConfig` struct
- `renderReportHTML(summary summaryResponse, analysis string) string` — builds HTML email with:
  - Subject: "DevPulse Daily Report — YYYY-MM-DD"
  - Section 1: Platform Summary table (counts + DoD/WoW/MoM with directional arrows ↑↓)
  - Section 2: Error repos table (if any)
  - Section 3: Infrastructure Analysis (Claude narrative, wrapped in `<pre>`)
- `renderReportText(summary summaryResponse, analysis string) string` — plain text fallback
- `sendEmail(ctx, cfg reportConfig, subject, html, text string) error` — POST to `https://api.sendgrid.com/v3/mail/send` with Bearer auth. Uses `analysisClient` (90s timeout). Request body follows SendGrid v3 format:
  ```json
  {
    "personalizations": [{"to": [{"email": "..."}]}],
    "from": {"email": "..."},
    "subject": "...",
    "content": [
      {"type": "text/plain", "value": "..."},
      {"type": "text/html", "value": "..."}
    ]
  }
  ```
- `handleReport(db, mcfg, rcfg)` handler:
  1. If `rcfg` is nil → 503
  2. Call `collectSummary(ctx, db)` (extracted from summary handler — shared logic)
  3. Call `collectAllMetrics(ctx, mcfg, token, defaultReportDays)` + `analyzeMetrics(ctx, mcfg, metrics)`
  4. Render HTML/text
  5. Send via SendGrid
  6. Return `reportResponse{Sent: true}`

Use `defaultReportDays = 1` for the daily report metrics window.

**Step 4: Run tests**

Run: `make test`
Expected: PASS

**Step 5: Commit**

```bash
git add pkg/admin/report.go pkg/admin/report_test.go
git commit -S -m "feat: add POST /report endpoint with SendGrid email"
```

---

### Task 7: Wire Report Route Into Admin Server

**Files:**
- Modify: `pkg/admin/admin.go`

**Step 1: Add report config loading and route**

In `Run()`, after `mcfg := newMetricsConfig()`:
```go
rcfg := loadReportConfig() // nil if env var missing — report returns 503
```

Add route:
```go
mux.HandleFunc("POST /report", handleReport(db, mcfg, rcfg))
```

**Step 2: Run tests**

Run: `make test`
Expected: PASS

**Step 3: Commit**

```bash
git add pkg/admin/admin.go
git commit -S -m "feat: wire POST /report route in admin server"
```

---

### Task 8: CLI Script — `tools/tenant-summary`

**Files:**
- Create: `tools/tenant-summary`

**Step 1: Write script**

Follow the same pattern as `tools/tenant-list`. The script:
1. Sources `common` for `err`, `msg`, `has_tools`
2. Requires `gcloud`, `terraform`, `curl`, `jq`
3. Gets admin URL from terraform output
4. Gets identity token from gcloud
5. Calls `GET /summary`
6. Formats JSON output as terminal table using jq

Output format:
```
DevPulse Platform Summary — 2026-04-11 (updated 14:30 UTC)

METRIC              CURRENT     DoD          WoW          MoM
Tenants             42          +1 (2.4%)    +5 (13.5%)   +12 (40.0%)
  Free              30          ...
  Starter           8           ...
  Pro               3           ...
  Enterprise        1           ...
Repos               156         ...
Events              89,432      ...
Contributors        1,203       ...
Installations       5           ...
Repos w/ Errors     2           ...

REPOS WITH ERRORS:
  ORG/REPO                ERRORS  LAST ERROR
  foo/bar                 3       rate limit exceeded
```

**Step 2: Make executable**

```bash
chmod +x tools/tenant-summary
```

**Step 3: Commit**

```bash
git add tools/tenant-summary
git commit -S -m "feat: add tools/tenant-summary CLI script"
```

---

### Task 9: Terraform — Secret, Scheduler, Env Mount

**Files:**
- Modify: `infra/saas/secrets.tf` — add `report-config` secret + IAM binding
- Modify: `infra/saas/cloudrun.tf` — mount secret as env var on admin service
- Modify: `infra/saas/scheduler.tf` — add daily report scheduler job
- Modify: `infra/saas/iam.tf` — grant deployer SA invoker on admin service (for scheduler)

**Step 1: Add secret to `secrets.tf`**

```hcl
resource "google_secret_manager_secret" "report_config" {
  secret_id = "${var.prefix}-report-config"
  project   = var.project_id

  replication {
    auto {}
  }

  depends_on = [google_project_service.default]
}

resource "google_secret_manager_secret_iam_member" "run_report_config" {
  secret_id = google_secret_manager_secret.report_config.id
  role      = "roles/secretmanager.secretAccessor"
  member    = "serviceAccount:${google_service_account.run.email}"
}
```

**Step 2: Mount on admin service in `cloudrun.tf`**

Add env block to the admin service containers block:
```hcl
env {
  name = "REPORT_CONFIG"
  value_source {
    secret_key_ref {
      secret  = google_secret_manager_secret.report_config.secret_id
      version = "latest"
    }
  }
}
```

**Step 3: Add scheduler job to `scheduler.tf`**

```hcl
resource "google_cloud_scheduler_job" "daily_report" {
  name     = "${var.prefix}-daily-report"
  schedule = "0 7 * * *"
  project  = var.project_id
  region   = var.region

  http_target {
    http_method = "POST"
    uri         = "${google_cloud_run_v2_service.admin.uri}/report"

    oidc_token {
      service_account_email = google_service_account.deployer.email
    }
  }

  depends_on = [google_project_service.default]
}
```

Note: The scheduler uses `oidc_token` (not `oauth_token`) because it's calling a Cloud Run service (not the Run admin API). The deployer SA needs `roles/run.invoker` on the admin service.

**Step 4: Add deployer invoker on admin service to `iam.tf`**

```hcl
resource "google_cloud_run_v2_service_iam_member" "deployer_admin_invoker" {
  name     = google_cloud_run_v2_service.admin.name
  location = var.region
  project  = var.project_id
  role     = "roles/run.invoker"
  member   = "serviceAccount:${google_service_account.deployer.email}"
}
```

**Step 5: Commit**

```bash
git add infra/saas/secrets.tf infra/saas/cloudrun.tf infra/saas/scheduler.tf infra/saas/iam.tf
git commit -S -m "infra: add report-config secret, daily scheduler, admin invoker"
```

---

### Task 10: Qualify & Final Verification

**Step 1: Run full qualification**

Run: `make qualify`
Expected: All tests pass, lint clean, no vulnerabilities

**Step 2: Manual smoke test (local)**

```bash
ADMIN_REQUIRE_IAM=false DATABASE_URL="..." go run ./cmd/devpulse-admin
# In another terminal:
curl -s localhost:8080/summary | jq .
curl -s -X POST localhost:8080/report
curl -s localhost:8080/metrics?days=1
```

**Step 3: Commit any fixes from qualification**

---

## Key Implementation Notes

- **Shared summary logic**: Extract `collectSummary(ctx, db) (summaryResponse, error)` from the handler so both `GET /summary` and `POST /report` can call it without HTTP overhead.
- **Metrics function reuse**: `collectAllMetrics` and `analyzeMetrics` in `metrics.go` are already pure functions — `POST /report` calls them directly.
- **SendGrid API**: Use raw HTTP POST to `https://api.sendgrid.com/v3/mail/send` — no SDK dependency. The `analysisClient` (90s timeout) is appropriate since email send is fast but the metrics collection preceding it is slow.
- **Report config**: `loadReportConfig()` reads `REPORT_CONFIG` env var. Returns `nil` if empty — `POST /report` returns 503 gracefully. `GET /summary` is independent.
- **Bot exclusion in contributors count**: Use `username NOT LIKE '%[bot]'` — consistent with existing `ContribExcludeSQL` but simplified since we're not joining the event table. The `developer` table only contains usernames that have authored events.
- **Error repos query**: Uses `tenant_repo` directly — `import_errors` already represents consecutive failures (resets on success).
- **Migration ordering**: File is `010_platform_stats.sql`. Migrations run in filename order via `applyMigrations` with advisory lock.
