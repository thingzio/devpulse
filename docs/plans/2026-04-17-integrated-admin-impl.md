# Integrated Admin Dashboard Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Eliminate the separate `devpulse-admin` service by integrating admin into `devpulse-site` with full SSR, session-based auth + username whitelist, CSRF protection, and multi-page lazy loading.

**Architecture:** Admin routes (`/admin/*`) added to the site router, gated by `RequireAdmin` middleware that validates the existing session cookie and checks `DEVPULSE_ADMIN_USERS` env var. All pages are server-rendered HTML with form POST + PRG. No admin JSON API. Heavy pages (tokens, metrics) are separate drill-downs.

**Tech Stack:** Go `html/template`, `crypto/rand` for CSRF, existing `pkg/tenant` + `pkg/middleware` packages.

**Design doc:** `docs/plans/2026-04-17-integrated-admin-design.md`

---

### Task 1: Add RequireAdmin middleware + CSRF helpers

**Files:**
- Create: `pkg/middleware/admin.go`
- Create: `pkg/middleware/admin_test.go`

**Step 1: Write the failing test**

Create `pkg/middleware/admin_test.go`:

```go
package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thingzio/devpulse/pkg/middleware"
)

func TestIsAdmin(t *testing.T) {
	t.Setenv("DEVPULSE_ADMIN_USERS", "alice,bob")
	assert.True(t, middleware.IsAdmin("alice"))
	assert.True(t, middleware.IsAdmin("bob"))
	assert.False(t, middleware.IsAdmin("eve"))
	assert.False(t, middleware.IsAdmin(""))
}

func TestIsAdmin_Empty(t *testing.T) {
	t.Setenv("DEVPULSE_ADMIN_USERS", "")
	assert.False(t, middleware.IsAdmin("alice"))
}

func TestIsAdmin_Whitespace(t *testing.T) {
	t.Setenv("DEVPULSE_ADMIN_USERS", " alice , bob ")
	assert.True(t, middleware.IsAdmin("alice"))
	assert.True(t, middleware.IsAdmin("bob"))
}

func TestRequireAdmin_NoCookie(t *testing.T) {
	handler := middleware.RequireAdmin(nil)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/admin", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestGenerateCSRFToken(t *testing.T) {
	tok := middleware.GenerateCSRFToken()
	require.NotEmpty(t, tok)
	assert.Len(t, tok, 44) // 32 bytes base64url = 44 chars
}

func TestValidateCSRF(t *testing.T) {
	tok := middleware.GenerateCSRFToken()
	assert.True(t, middleware.ValidateCSRF(tok, tok))
	assert.False(t, middleware.ValidateCSRF(tok, "wrong"))
	assert.False(t, middleware.ValidateCSRF(tok, ""))
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./pkg/middleware/ -run "TestIsAdmin|TestRequireAdmin_NoCookie|TestGenerateCSRF|TestValidateCSRF" -v`
Expected: FAIL — functions not defined

**Step 3: Write minimal implementation**

Create `pkg/middleware/admin.go`:

```go
package middleware

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/thingzio/devpulse/pkg/tenant"
)

// RequireAdmin validates the session cookie and checks the username against
// the DEVPULSE_ADMIN_USERS whitelist. Returns 404 (not 403) to hide the
// route from non-admins.
func RequireAdmin(db *sql.DB) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cookie, err := r.Cookie(SessionCookieName())
			if err != nil {
				http.NotFound(w, r)
				return
			}

			tn, err := tenant.ValidateSession(r.Context(), db, cookie.Value)
			if err != nil || tn == nil || !IsAdmin(tn.Username) {
				if err != nil {
					slog.Warn("admin access denied",
						"path", r.URL.Path,
						"remote", r.RemoteAddr,
						"error", err)
				} else {
					slog.Warn("admin access denied",
						"path", r.URL.Path,
						"remote", r.RemoteAddr)
				}
				http.NotFound(w, r)
				return
			}

			ctx := WithTenantContext(r.Context(), tn)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// IsAdmin checks whether username is in the DEVPULSE_ADMIN_USERS env var.
func IsAdmin(username string) bool {
	raw := os.Getenv("DEVPULSE_ADMIN_USERS")
	if raw == "" {
		return false
	}
	for u := range strings.SplitSeq(raw, ",") {
		if strings.TrimSpace(u) == username {
			return true
		}
	}
	return false
}

// GenerateCSRFToken produces a 32-byte random token encoded as base64url.
func GenerateCSRFToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		slog.Error("failed to generate CSRF token", "error", err)
		return ""
	}
	return base64.URLEncoding.EncodeToString(b)
}

// ValidateCSRF performs constant-time comparison of CSRF tokens.
func ValidateCSRF(expected, actual string) bool {
	if expected == "" || actual == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(expected), []byte(actual)) == 1
}

// AdminAuditLog logs admin actions for auditability.
func AdminAuditLog(action string, ctx context.Context, path, remoteAddr, detail string) {
	username := "anonymous"
	if tn := TenantFromContext(ctx); tn != nil {
		username = tn.Username
	}
	slog.Warn("admin action",
		"action", action,
		"admin", username,
		"path", path,
		"remote", remoteAddr,
		"detail", detail,
	)
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./pkg/middleware/ -run "TestIsAdmin|TestRequireAdmin_NoCookie|TestGenerateCSRF|TestValidateCSRF" -v`
Expected: PASS

**Step 5: Commit**

```bash
git add pkg/middleware/admin.go pkg/middleware/admin_test.go
git commit -S -m "feat: add RequireAdmin middleware with CSRF helpers"
```

---

### Task 2: Move admin summary + types to site package

**Files:**
- Create: `pkg/server/admin_summary.go` (from `pkg/admin/summary.go`)
- Create: `pkg/server/admin_types.go` (from `pkg/admin/types.go`, pruned)
- Create: `pkg/server/admin_summary_test.go` (from `pkg/admin/summary_test.go`)

**Step 1: Create `pkg/server/admin_types.go`**

Copy types from `pkg/admin/types.go` into `pkg/server/admin_types.go`.
Change `package admin` → `package server`.
Remove report-related types: `reportResponse`, `reportConfig`.
Keep: `upgradeRequest`, `upgradeResponse`, `tenantSummary`, `tenantDetail`,
`repoDetail`, `resetErrorsRequest`, `resetErrorsResponse`, `inviteRequest`,
`inviteResponse`, `tokenStatus`, `platformStats`, `statsDelta`, `errorRepo`,
`summaryResponse`.

**Step 2: Create `pkg/server/admin_summary.go`**

Copy `collectStats`, `upsertStats`, `getStats`, `getErrorRepos`, `computeDelta`,
`collectSummary` from `pkg/admin/summary.go`.
Change `package admin` → `package server`.
Remove `handleSummary` handler (will be rewritten as SSR in Task 4).

**Step 3: Create `pkg/server/admin_summary_test.go`**

Copy tests from `pkg/admin/summary_test.go`, update package to `server`.
Verify `computeDelta` logic is tested.

**Step 4: Run tests**

Run: `go test ./pkg/server/ -run "TestComputeDelta" -v`
Expected: PASS

**Step 5: Commit**

```bash
git add pkg/server/admin_types.go pkg/server/admin_summary.go pkg/server/admin_summary_test.go
git commit -S -m "feat: move admin summary and types to server package"
```

---

### Task 3: Move admin metrics to site package

**Files:**
- Create: `pkg/server/admin_metrics.go` (from `pkg/admin/metrics.go`)

**Step 1: Copy `pkg/admin/metrics.go` → `pkg/server/admin_metrics.go`**

Change `package admin` → `package server`.
Remove `handleMetricsReview` handler (will be rewritten as SSR in Task 4).
Keep: `metricsConfig`, `newMetricsConfig`, `gcpAccessToken`, `queryTimeSeries`,
`metricQuery`, `metricQueries`, `trendQueries`, `collectAllMetrics`,
`formatTimeSeries`, `analyzeMetrics`, all constants.

**Step 2: Verify it compiles**

Run: `go build ./pkg/server/`
Expected: Success

**Step 3: Commit**

```bash
git add pkg/server/admin_metrics.go
git commit -S -m "feat: move admin metrics to server package"
```

---

### Task 4: Create admin SSR handlers

**Files:**
- Create: `pkg/server/handler_admin.go`
- Create: `pkg/server/handler_admin_test.go`

**Step 1: Write test for admin dashboard handler**

Create `pkg/server/handler_admin_test.go` with tests:
- `TestAdminDashboard_NoSession` → expects 404
- `TestAdminDashboard_NonAdmin` → expects 404
- Test CSRF token presence in rendered forms

These tests will need a test DB. Use `setupTestDB(t)` if available, or test
at the middleware level with mocked sessions.

**Step 2: Write handlers**

Create `pkg/server/handler_admin.go` with all admin page handlers.
Each handler:
1. Loads data from DB
2. Renders HTML template via `renderTemplate`
3. POST handlers validate CSRF, execute action, audit log, redirect (PRG)

Handlers to implement:
- `adminDashboardHandler(db)` — GET `/admin` — collectSummary → render `admin.html`
- `adminTenantsHandler(db)` — GET `/admin/tenants` — ListTenantSummaries → render `admin_tenants.html`
- `adminTenantDetailHandler(db)` — GET `/admin/tenant/{username}` — GetTenantDetailByUsername + repos → render `admin_tenant.html`
- `adminTokensHandler(db)` — GET `/admin/tokens` — mint tokens + rate limit check → render `admin_tokens.html`
- `adminMetricsHandler(mcfg)` — GET `/admin/metrics` — collectAllMetrics + analyzeMetrics → render `admin_metrics.html`
- `adminUpdatePlanHandler(db)` — POST `/admin/tenant/{username}/plan` — validate CSRF, UpdatePlan, audit, redirect
- `adminInviteHandler(db)` — POST `/admin/tenant/{username}/invite` — validate CSRF, InsertMinimalTenant + UpdatePlan, audit, redirect
- `adminResetErrorsHandler(db)` — POST `/admin/tenant/{username}/reset` — validate CSRF, ResetImportErrorsByRepo, audit, redirect
- `adminHardResetHandler(db)` — POST `/admin/tenant/{username}/hard-reset` — validate CSRF, HardResetRepo, audit, redirect

Each POST handler pattern:
```go
func adminUpdatePlanHandler(db *sql.DB) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        tn := middleware.TenantFromContext(r.Context())
        csrf := r.FormValue("csrf_token")
        if !middleware.ValidateCSRF(tn.CSRFToken, csrf) {
            http.NotFound(w, r)
            return
        }
        r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
        // ... parse form, validate, execute, audit log ...
        middleware.AdminAuditLog("update_plan", r.Context(), r.URL.Path, r.RemoteAddr,
            fmt.Sprintf("username=%s plan=%s", username, newPlan))
        http.Redirect(w, r, "/admin/tenant/"+username, http.StatusSeeOther)
    }
}
```

**Step 3: Run tests**

Run: `go test ./pkg/server/ -run "TestAdmin" -v`
Expected: PASS

**Step 4: Commit**

```bash
git add pkg/server/handler_admin.go pkg/server/handler_admin_test.go
git commit -S -m "feat: add admin SSR handlers with CSRF and audit logging"
```

---

### Task 5: Create admin HTML templates

**Files:**
- Create: `pkg/server/templates/admin.html`
- Create: `pkg/server/templates/admin_tenants.html`
- Create: `pkg/server/templates/admin_tenant.html`
- Create: `pkg/server/templates/admin_tokens.html`
- Create: `pkg/server/templates/admin_metrics.html`

**Step 1: Create templates**

All templates extend `layout.html` using `{{define "content"}}...{{end}}`.
Each includes an admin nav bar at the top:
```html
<nav class="admin-nav">
    <a href="/admin">Summary</a>
    <a href="/admin/tenants">Tenants</a>
    <a href="/admin/tokens">Tokens</a>
    <a href="/admin/metrics">Metrics</a>
</nav>
```

Templates:
- `admin.html` — Platform Summary table (Current + DoD/WoW/MoM), error repos
- `admin_tenants.html` — Tenant table (searchable via JS filter, sortable)
- `admin_tenant.html` — Tenant profile, repo table, inline forms for plan/reset/hard-reset (each with hidden CSRF field)
- `admin_tokens.html` — Token pool table (login, installation_id, limit, used, remaining, reset_at)
- `admin_metrics.html` — Infrastructure Analysis (rendered HTML from Anthropic)

All forms include:
```html
<input type="hidden" name="csrf_token" value="{{.CSRFToken}}">
```

Style with existing `app.css` classes. Use `.tbl-home` pattern for tables.

**Step 2: Register templates in `init()`**

In `pkg/server/server.go:53-64`, add admin templates to the `simplePages` slice:

```go
simplePages := []string{
    "landing.html", "tos.html", "help.html", "settings.html", "changelog.html",
    "admin.html", "admin_tenants.html", "admin_tenant.html",
    "admin_tokens.html", "admin_metrics.html",
}
```

**Step 3: Verify templates compile**

Run: `go build ./pkg/server/`
Expected: Success (templates are embedded at build time)

**Step 4: Commit**

```bash
git add pkg/server/templates/admin*.html pkg/server/server.go
git commit -S -m "feat: add admin HTML templates with CSRF fields"
```

---

### Task 6: Wire admin routes into site router

**Files:**
- Modify: `pkg/server/server.go:168-272` (makeRouter function)

**Step 1: Add admin routes to makeRouter**

After the data API section (line ~267), before `return mux` (line 272), add:

```go
// Admin routes (session auth + username whitelist)
requireAdmin := middleware.RequireAdmin(db)
adminWrap := func(h http.HandlerFunc) http.Handler {
    return requireAdmin(h)
}
mcfg := newMetricsConfig()
mux.Handle("GET /admin", adminWrap(adminDashboardHandler(db)))
mux.Handle("GET /admin/tenants", adminWrap(adminTenantsHandler(db)))
mux.Handle("GET /admin/tenant/{username}", adminWrap(adminTenantDetailHandler(db)))
mux.Handle("POST /admin/tenant/{username}/plan", adminWrap(adminUpdatePlanHandler(db)))
mux.Handle("POST /admin/tenant/{username}/invite", adminWrap(adminInviteHandler(db)))
mux.Handle("POST /admin/tenant/{username}/reset", adminWrap(adminResetErrorsHandler(db)))
mux.Handle("POST /admin/tenant/{username}/hard-reset", adminWrap(adminHardResetHandler(db)))
mux.Handle("GET /admin/tokens", adminWrap(adminTokensHandler(db)))
mux.Handle("GET /admin/metrics", adminWrap(adminMetricsHandler(mcfg)))
```

**Step 2: Run full test suite**

Run: `make test`
Expected: PASS

**Step 3: Commit**

```bash
git add pkg/server/server.go
git commit -S -m "feat: wire admin routes into site router"
```

---

### Task 7: Add DEVPULSE_ADMIN_USERS to Terraform site service

**Files:**
- Modify: `infra/saas/cloudrun.tf` — add env var to site service container
- Modify: `infra/saas/variables.tf` — add `admin_users` variable

**Step 1: Add variable**

In `infra/saas/variables.tf`, add:
```hcl
variable "admin_users" {
  description = "Comma-separated GitHub usernames allowed admin access"
  type        = string
  default     = "mchmarny"
}
```

**Step 2: Add env var to site service**

In `infra/saas/cloudrun.tf`, in the `google_cloud_run_v2_service.serve`
container env block, add:
```hcl
env {
  name  = "DEVPULSE_ADMIN_USERS"
  value = var.admin_users
}
```

**Step 3: Verify TF validates**

Run: `cd infra/saas && terraform validate`
Expected: Success

**Step 4: Commit**

```bash
git add infra/saas/cloudrun.tf infra/saas/variables.tf
git commit -S -m "feat: add DEVPULSE_ADMIN_USERS env var to site service"
```

---

### Task 8: Remove admin service from Terraform

**Files:**
- Modify: `infra/saas/cloudrun.tf` — remove `google_cloud_run_v2_service.admin` resource + IAM bindings
- Modify: `infra/saas/scheduler.tf` — remove report scheduler job
- Modify: `infra/saas/iam.tf` — remove `deployer_admin_invoker` IAM binding
- Modify: `infra/saas/variables.tf` — remove `admin_invoker_emails` variable
- Modify: `infra/saas/outputs.tf` — remove `admin_url` output

**Step 1: Remove resources**

Remove from `cloudrun.tf`:
- `google_cloud_run_v2_service.admin` resource (starts line ~260)
- `google_cloud_run_v2_service_iam_member.admin_invoker` resource (starts line ~398)

Remove from `scheduler.tf`:
- The scheduler job that hits `admin.uri/report` (line ~28 reference)

Remove from `iam.tf`:
- `google_cloud_run_v2_service_iam_member.deployer_admin_invoker` (line ~132)

Remove from `variables.tf`:
- `admin_invoker_emails` variable (line ~49)

Remove from `outputs.tf`:
- `admin_url` output (line ~21)

**Step 2: Verify TF validates**

Run: `cd infra/saas && terraform validate`
Expected: Success

**Step 3: Commit**

```bash
git add infra/saas/cloudrun.tf infra/saas/scheduler.tf infra/saas/iam.tf infra/saas/variables.tf infra/saas/outputs.tf
git commit -S -m "infra: remove admin Cloud Run service and scheduler"
```

**Note:** The actual `terraform apply` to destroy the admin service should be
done manually after the site deploy is confirmed working. Run:
```bash
terraform state rm google_cloud_run_v2_service.admin
```
Or let `terraform apply` handle the destroy. Either way, do this carefully
after the new admin routes are deployed and verified.

---

### Task 9: Remove admin binary, package, and goreleaser target

**Files:**
- Delete: `cmd/devpulse-admin/` (entire directory)
- Delete: `pkg/admin/` (entire directory)
- Modify: `.goreleaser.yaml` — remove `devpulse-admin` build + ko image

**Step 1: Delete admin binary**

```bash
rm -rf cmd/devpulse-admin/
```

**Step 2: Delete admin package**

```bash
rm -rf pkg/admin/
```

**Step 3: Remove from goreleaser**

In `.goreleaser.yaml`, remove the `devpulse-admin` build block (lines ~42-51)
and the `devpulse-admin` ko image block (lines ~92-99).

**Step 4: Remove AdminRequireIAM from config**

In `pkg/config/env.go`, remove `AdminRequireIAM()` function (line ~161-165)
and the associated `// Admin` comment block (line ~158).

**Step 5: Verify build**

Run: `make build`
Expected: Success (only `devpulse-site` and `devpulse-import` built)

**Step 6: Commit**

```bash
git add -A
git commit -S -m "chore: remove admin binary, package, and goreleaser target"
```

---

### Task 10: Remove admin CLI tools

**Files:**
- Delete: `tools/tenant-upgrade`
- Delete: `tools/tenant-list`
- Delete: `tools/tenant-detail`
- Delete: `tools/tenant-invite`
- Delete: `tools/tenant-tokens`
- Delete: `tools/tenant-summary`
- Delete: `tools/metrics-review`
- Delete: `tools/repo-reset`

**Step 1: Delete CLI tools**

```bash
rm tools/tenant-upgrade tools/tenant-list tools/tenant-detail \
   tools/tenant-invite tools/tenant-tokens tools/tenant-summary \
   tools/metrics-review tools/repo-reset
```

**Step 2: Verify remaining tools**

```bash
ls tools/
```

Expected remaining: `bump`, `common`, `db-attach`, `e2e`, `job-detail`,
`setup-gh-env`, `setup-tools`

**Step 3: Commit**

```bash
git add -A
git commit -S -m "chore: remove admin CLI tools replaced by admin dashboard"
```

---

### Task 11: Run full qualification

**Step 1: Run make qualify**

Run: `make qualify`
Expected: All tests pass, no lint errors, no vulnerabilities.

**Step 2: Fix any issues**

If any failures, fix and re-run until clean.

**Step 3: Run dev server manually**

```bash
DEVPULSE_ADMIN_USERS=mchmarny make server
```

Navigate to `http://localhost:8080/admin` and verify:
- Dashboard loads with platform summary
- Tenants page shows tenant list
- Tokens page shows token status
- Metrics page runs live analysis
- Non-admin user gets 404
- No-cookie request gets 404
- CSRF tokens present in all forms
- Plan upgrade form works (PRG redirect)

**Step 4: Final commit if any fixes**

```bash
git add -A
git commit -S -m "fix: resolve qualification issues from admin integration"
```

---

### Task 12: Update CLAUDE.md and docs

**Files:**
- Modify: `.claude/CLAUDE.md` — update Architecture section, remove admin binary references
- Modify: `docs/ARCHITECTURE.md` (if exists) — update
- Modify: `docs/ADMIN.md` (if exists) — update to reference web UI instead of CLI tools

**Step 1: Update project docs**

Remove references to:
- `cmd/devpulse-admin/` binary
- `tools/tenant-*` and `tools/metrics-review` and `tools/repo-reset`
- Admin Cloud Run service
- Admin scheduler
- `ADMIN_REQUIRE_IAM`, `SEND_API_KEY`, `REPORT_*` env vars

Add references to:
- `/admin` routes on site service
- `DEVPULSE_ADMIN_USERS` env var
- `RequireAdmin` middleware in `pkg/middleware/admin.go`

**Step 2: Commit**

```bash
git add -A
git commit -S -m "docs: update project docs for integrated admin"
```
