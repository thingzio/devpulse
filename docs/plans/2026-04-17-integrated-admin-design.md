# Integrated Admin Dashboard Design

**Date:** 2026-04-17
**Status:** Approved

## Goal

Eliminate the separate `devpulse-admin` Cloud Run service by integrating admin
functionality into the `devpulse-site` binary. This removes one binary, one
container image, one Cloud Run service, one scheduler job, and ~10 CLI tools.
Adopt DevTrace's security patterns (session-based auth, username whitelist,
404 on denied access, audit logging).

## Decisions

- **Admin in site binary** — single deploy, session-based auth, no IAM tokens
- **Full SSR** — no admin JSON API surface; all GET render HTML, all POST are
  form submissions with PRG redirects
- **CSRF tokens** — per-session random token validated on every admin POST
- **Multi-page lazy loading** — `/admin` is cheap summary; tokens, metrics,
  tenant detail are separate pages loaded on navigation
- **Daily report removed** — no email, no Sendinblue, no scheduler
- **Platform stats kept** — `devpulse_platform_stats` table stays; upserted
  on each `/admin` visit for DoD/WoW/MoM delta tracking
- **Metrics review on-demand** — live GCP Monitoring + Anthropic analysis,
  no pre-generation

## Security Model

| Measure | Implementation |
|---------|---------------|
| Auth gate | Session cookie (same OAuth) + `DEVPULSE_ADMIN_USERS` env var whitelist |
| 404 not 403 | `http.NotFound()` for non-admins — hides route existence |
| CSRF tokens | Random per-session token in hidden form field, validated on POST |
| Audit logging | `slog.Warn("admin action", ...)` with action, admin, path, remote, detail |
| No admin role in DB | Whitelist is env-only — no DB column, no session escalation |
| Full SSR | No admin JSON endpoints — zero client-side API surface |
| Cookie security | `__Host-session` prefix (HTTPS), HttpOnly, SameSite=Lax |
| MaxBytesReader | All POST handlers cap request body at 1MB |

### RequireAdmin middleware (`pkg/middleware/admin.go`)

```go
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
                slog.Warn("admin access denied",
                    "path", r.URL.Path,
                    "remote", r.RemoteAddr)
                http.NotFound(w, r)
                return
            }
            ctx := WithTenantContext(r.Context(), tn)
            next.ServeHTTP(w, r.WithContext(ctx))
        })
    }
}

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
```

## Routes

All wrapped with `requireAdmin` middleware.

### Read pages (GET → server-rendered HTML)

```
GET /admin              → Platform Summary (Current + DoD/WoW/MoM deltas, error repos)
GET /admin/tenants      → Tenant list (searchable, sortable table)
GET /admin/tenant/{username} → Tenant detail (profile, repos, backfill, errors, inline actions)
GET /admin/tokens       → Token pool status (per-installation quota, remaining, reset)
GET /admin/metrics      → Infrastructure Analysis (live GCP Monitoring → Anthropic)
```

### Write actions (POST → process → redirect)

```
POST /admin/tenant/{username}/plan       → Update plan (form: plan) → redirect to tenant detail
POST /admin/tenant/{username}/invite     → Invite tenant (form: plan) → redirect to tenant detail
POST /admin/tenant/{username}/reset      → Reset import errors (form: org, repo) → redirect
POST /admin/tenant/{username}/hard-reset → Hard reset repo (form: org, repo) → redirect
```

All POST handlers:
1. Validate CSRF token
2. Parse form body (MaxBytesReader 1MB)
3. Execute operation
4. Audit log via `slog.Warn`
5. Redirect back to referring page (PRG pattern)

## Page Load Profiles

| Page | Data | Cost |
|------|------|------|
| `/admin` | `collectStats` + upsert today + compare yesterday/7d/30d + error repos | 4 cheap DB queries + 1 upsert |
| `/admin/tenants` | `tenant.ListTenantSummaries()` | 1 light query |
| `/admin/tenant/{user}` | Tenant detail + repo details + backfill status | 2-3 queries |
| `/admin/tokens` | Mint installation tokens + `GET /rate_limit` per token | N GitHub API calls (~2-5s) |
| `/admin/metrics` | 23 GCP Monitoring queries + Anthropic analysis | ~10-15s |

## Templates

Five new templates in `pkg/server/templates/`:

- `admin.html` — Platform Summary dashboard (landing)
- `admin_tenants.html` — Tenant list with search/sort
- `admin_tenant.html` — Tenant detail with inline form actions
- `admin_tokens.html` — Token pool table
- `admin_metrics.html` — Infrastructure Analysis (rendered Anthropic output)

Each extends the existing header/footer pattern. Navigation between admin pages
via plain `<a>` links in a simple admin nav bar.

## Code Migration

### Move to `pkg/server/` (as handler files)

| Source | Destination | Notes |
|--------|-------------|-------|
| `pkg/admin/admin.go` handlers | `pkg/server/handler_admin.go` | Rewrite as SSR (render templates, not JSON) |
| `pkg/admin/metrics.go` | `pkg/server/admin_metrics.go` | GCP queries + Anthropic analysis unchanged |
| `pkg/admin/summary.go` | `pkg/server/admin_summary.go` | Stats collection + delta computation |
| `pkg/admin/types.go` | `pkg/server/admin_types.go` | Prune report types |
| `pkg/admin/admin_test.go` | `pkg/server/handler_admin_test.go` | Adapt for SSR responses |

### New files

| File | Purpose |
|------|---------|
| `pkg/middleware/admin.go` | `RequireAdmin` middleware + `IsAdmin` + CSRF helpers |
| `pkg/server/templates/admin*.html` (5 files) | Admin page templates |

### Delete entirely

| What | Why |
|------|-----|
| `cmd/devpulse-admin/` | Separate binary gone |
| `pkg/admin/` | Package dissolved — code migrated or deleted |
| `pkg/admin/report.go` + `report_test.go` | Email report removed |
| `tools/tenant-upgrade` | Replaced by `/admin/tenant/{user}/plan` form |
| `tools/tenant-list` | Replaced by `/admin/tenants` page |
| `tools/tenant-detail` | Replaced by `/admin/tenant/{user}` page |
| `tools/tenant-invite` | Replaced by `/admin/tenant/{user}/invite` form |
| `tools/tenant-tokens` | Replaced by `/admin/tokens` page |
| `tools/tenant-summary` | Replaced by `/admin` dashboard |
| `tools/metrics-review` | Replaced by `/admin/metrics` page |
| `tools/repo-reset` | Replaced by `/admin/tenant/{user}/reset` form |

### Terraform changes (`infra/saas/`)

| File | Change |
|------|--------|
| `cloudrun.tf` | Remove `google_cloud_run_v2_service.admin` resource |
| `cloudrun.tf` | Add `DEVPULSE_ADMIN_USERS` env var to site service |
| `scheduler.tf` | Remove admin report scheduler job |
| `iam.tf` | Remove `admin_invoker_emails` IAM bindings |
| `variables.tf` | Remove `admin_invoker_emails`, `report_to_email`, `report_from_email` vars |
| `outputs.tf` | Remove `admin_url` output |
| `main.tf` | Remove admin-related secret versions if any |

### goreleaser

Remove `devpulse-admin` build target and container image.

## What's NOT Changing

- Import job stays as separate Cloud Run Job
- `devpulse_platform_stats` table schema unchanged
- DB pool config for site unchanged (admin queries are lightweight)
- `monitoring.viewer` role stays on run SA (needed for metrics review)
- GitHub App key secret volume stays on site service (needed for token status)

## CSRF Implementation

```go
// Generate: store in session, embed in forms
func generateCSRFToken() string {
    b := make([]byte, 32)
    _, _ = rand.Read(b)
    return base64.URLEncoding.EncodeToString(b)
}

// Validate: constant-time compare on every POST
func validateCSRF(r *http.Request, sessionToken string) bool {
    formToken := r.FormValue("csrf_token")
    return subtle.ConstantTimeCompare([]byte(formToken), []byte(sessionToken)) == 1
}
```

CSRF token stored in the session (alongside the existing session cookie value)
or as a separate HttpOnly cookie. Embedded as `<input type="hidden"
name="csrf_token" value="...">` in every admin form.

## Environment Variables

### Added to site service
- `DEVPULSE_ADMIN_USERS` — comma-separated GitHub usernames (e.g. `mchmarny`)

### Removed (were admin-only)
- `SEND_API_KEY`
- `REPORT_TO_EMAIL`
- `REPORT_FROM_EMAIL`
- `REPORT_SUBJECT_PREFIX`
- `ADMIN_REQUIRE_IAM`

### Already on site service (no change needed)
- `ANTHROPIC_API_KEY` — already used for import insights
- `GCP_PROJECT_ID` — already available
- `GITHUB_APP_ID` + `GITHUB_APP_KEY_PATH` — already mounted
