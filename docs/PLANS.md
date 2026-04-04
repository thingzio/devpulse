# Plan Feature Gating

> Status: **Implemented**

This document defines the plan tiers, per-feature gating, and implementation options for devpulse SaaS.

## Plan Matrix

|                          | Free        | Starter ($2.99/mo)| Pro ($4.99/mo)          | Enterprise (Custom)     |
|--------------------------|-------------|-------------------|-------------------------|-------------------------|
| **Repos**                | 1           | 5                 | 25                      | Unlimited               |
| **Private Repos**        | -           | -                 | -                       | Yes                     |
| **Events/Week**          | 500         | 2,500             | 15,000                  | Unlimited               |
| **Data Retention**       | 3 months    | 1 year            | 3 years                 | Unlimited               |
| **Data Export (PDF)**    | -           | Yes               | Yes                     | Yes                     |
| **Data Export (CSV/ZIP)**| -           | -                 | Yes                     | Yes                     |
| **AI**                   | -           | Insights          | Insights + Action Items | Insights + Action Items |
| **Reputation Score**     | Shallow     | Shallow           | Deep                    | Deep                    |
| **API Access**           | -           | -                 | -                       | Yes                     |
| **Import Frequency**     | Daily       | Hourly            | Hourly                  | Hourly + On-demand      |
| **Dedicated Instance**   | -           | -                 | -                       | Optional (quoted)       |

## Plan Definition Changes

Current plan constants live in `pkg/plan/plan.go` with `Limits{MaxRepos, MaxEventsPerWeek}`.

New `Limits` struct (proposed):

```go
type Limits struct {
    MaxRepos            int  // 0 = unlimited
    MaxEventsPerWeek    int  // 0 = unlimited
    MaxDataRangeMonths  int  // 0 = unlimited
    AILevel             int  // 0=none, 1=insights, 2=insights+actions
    DeepReputation      bool
    PDFExport           bool
    CSVExport           bool
    PrivateRepos        bool
    APIAccess           bool
    ImportIntervalHours int  // 24=daily, 1=hourly
    OnDemandImport      bool
}
```

A new `Starter` plan constant is added between `Free` and `Pro`.

---

## Feature Details

### 1. Repos (1 / 5 / 25 / Unlimited)

**Effort:** Trivial

Repo-add gating already exists. Update the constants in `pkg/plan/plan.go`.

**Changes:**
- `pkg/plan/plan.go` — update `All` map with new limits per tier

### 2. Data Retention (3 months / 1 year / 3 years / Unlimited)

**Effort:** Low

This is a UI-level date range clamp, not data purging. Most data queries already accept time range parameters. The server enforces a floor based on the tenant's plan.

**Changes:**
- `pkg/plan/plan.go` — add `MaxDataRangeMonths` to `Limits` (3, 12, 36, 0)
- Data API handlers — clamp the requested date range to the plan's maximum before passing to store queries
- Frontend — disable date picker options beyond the plan's allowed range

**Implementation:** Server-side clamp (compute earliest allowed date in each data handler, pass as floor to store query — tamper-proof) plus client-side date picker restrictions (disable ranges beyond plan limit — avoids confusing empty responses). Both layers are straightforward.

### 3. Data Export — PDF (Starter+) and CSV/ZIP (Pro+)

**Effort:** Low-Medium

Both export types live under a new **Export tab** in the dashboard. The tab is hidden for Free users. Users select "All repos" or specific repos from their imported list before exporting.

**PDF export** is client-side via jsPDF in `pkg/server/static/js/app.js:2570`. No server cost. Currently triggered from the dashboard toolbar — moves to the Export tab.

**CSV/ZIP export** is a new server-side handler. All data is already available via existing store methods.

**Changes:**
- Templates — new Export tab/page with repo selector (multi-select from imported repos, or "All")
- Templates — PDF and CSV download buttons on the Export tab, gated by plan (PDF: Starter+, CSV: Pro+)
- `pkg/server/data.go` — new `csvExportHandler` accepting repo list, returning a ZIP archive
- `pkg/server/server.go` — register `GET /data/export` with `scopedWrap`
- `pkg/plan/plan.go` — add `PDFExport bool` and `CSVExport bool` to Limits
- Frontend JS — move `generatePDF` trigger to Export tab, add repo selection logic

**CSV implementation:**

Single ZIP with multiple CSVs per selected repo. One endpoint, one request. Uses Go stdlib `encoding/csv` + `archive/zip`. ~150-200 lines.

ZIP structure:
```
export-2026-04-02/
  org-repo1/
    events.csv
    developers.csv
    insights.csv
    reputation.csv
  org-repo2/
    events.csv
    ...
```

**Datasets per repo:**
- Event search results (commits, PRs, issues)
- Developer list with reputation scores
- Insight time-series (PR velocity, review latency, retention, etc.)
- Repository metadata and overview metrics

**Export date range:** User-configurable up to the plan's maximum data retention window. The server clamps the requested range the same way dashboard queries are clamped (see §2).

### 4. AI (None / Insights / Insights + Action Items)

**Effort:** Low-Medium

The Claude API call happens in `pkg/importer/importer.go:240` via `data.GenerateInsights()`. The prompt is assembled in `pkg/data/insights_gen.go:177`.

**Changes:**
- `pkg/plan/plan.go` — add `AILevel int` (0=none, 1=insights, 2=insights+actions)
- `pkg/importer/importer.go` — skip `GenerateInsights` call when `AILevel == 0`
- `pkg/data/insights_gen.go` — vary the prompt based on AI level

**Implementation:** Always generate full insights + action items (single prompt, single stored result). Gate at the display layer — only show action items to Pro+ tenants. This avoids prompt-variation complexity, especially when the same repo is tracked by tenants on different plans.

**Cost impact:** Free tenants make zero Claude API calls. Starter/Pro/Enterprise all generate the same full response — the cost difference is gated by whether AI is enabled at all (`AILevel == 0` for Free).

### 5. Reputation Score (Shallow / Deep)

**Effort:** Low

Deep reputation is computed in `pkg/importer/importer.go:207` via `store.ImportDeepReputation()`, which calls the GitHub API per-user through the `reputer` library. Shallow reputation uses only locally stored commit/PR counts — no external API calls.

**Changes:**
- `pkg/plan/plan.go` — add `DeepReputation bool`
- `pkg/importer/importer.go` — skip `ImportDeepReputation` call when `DeepReputation == false`
- Frontend — optionally show a "Shallow" badge on the reputation chart for Free users

**Cost impact:** Deep reputation makes multiple GitHub API calls per user (profile, repos, contributions). Gating this for Free saves significant GitHub API quota.

### 6. API Access (Enterprise)

**Effort:** Low-Medium

The `/data/*` endpoints already return JSON and are RLS-scoped via `ScopedStoreMiddleware`. They currently require session cookie auth (`RequireAuth` in `pkg/middleware/auth.go`). API access adds an alternative auth path using GitHub Personal Access Tokens (PATs) — no custom key storage needed.

**Auth chain today:**
```
Request → RequireAuth (cookie → ValidateSession → tenant) → ScopedStoreMiddleware (conn → set_config → scoped Store) → handler
```

**Auth chain with GitHub PAT:**
```
Request → RequireAuthOrToken (cookie OR Bearer <github PAT> → resolve GitHub user → tenant) → ScopedStoreMiddleware (unchanged) → handler
```

The key insight is that `ScopedStoreMiddleware` only needs a `*tenant.Tenant` in context — it doesn't care how the tenant was resolved. By using GitHub PATs, devpulse delegates token lifecycle (creation, scoping, revocation) entirely to GitHub. No custom key storage, no key management UI.

**How it works:**

1. User creates a GitHub PAT (classic or fine-grained) — no special scopes required, just `read:user` to resolve identity
2. API request includes `Authorization: Bearer ghp_...`
3. Middleware calls GitHub `GET /user` with the token → gets GitHub user ID
4. Look up tenant via `getTenantByGitHubIDSQL` (already exists)
5. Check tenant plan allows API access → inject tenant into context
6. `ScopedStoreMiddleware` handles RLS as usual

**Token resolution caching:**

GitHub API call per request is expensive. Cache the `token_hash → (github_id, resolved_at)` mapping in-memory with a short TTL (e.g., 5 minutes). This means:
- First request: GitHub API call to validate token and resolve user
- Subsequent requests within TTL: cache hit, no GitHub call
- Revoked tokens stop working within TTL window (acceptable trade-off)
- Cache key is SHA-256 of the token (never store plaintext)

**Changes:**

- `pkg/middleware/auth.go` — extend `RequireAuth` (or add `RequireAuthOrToken`) to check `Authorization: Bearer ghp_...` header before falling back to cookie. On match, resolve GitHub user via API, look up tenant, inject into context. On token auth, return 401/403 JSON instead of redirect.
- `pkg/middleware/token_cache.go` — in-memory token resolution cache (map + mutex + TTL eviction). ~50-80 lines.
- `pkg/server/server.go` — swap `RequireAuth` for `RequireAuthOrToken` on the data route group (no route changes needed)
- `pkg/plan/plan.go` — add `APIAccess bool` to `Limits`

**Implementation options:**

1. **Extend existing auth middleware (recommended):** Modify `RequireAuth` to first check for `Authorization: Bearer ghp_...` header. If present, resolve via GitHub API (cached), look up tenant, inject into context. Fall back to cookie auth if no header. `ScopedStoreMiddleware` remains unchanged. All 30+ `/data/*` endpoints get API access for free — no route duplication. ~100-150 lines of new Go code (significantly less than custom key management).
2. **Separate `/api/v1/` prefix:** Mount a second route group with its own middleware stack. Cleaner separation but duplicates route registration. Only justified if the API needs different response shapes than the dashboard.

**Rate limiting:**

- Start simple: per-tenant limit (e.g., 100 req/min for Pro, configurable for Enterprise)
- Implement with an in-memory token bucket per tenant ID (stdlib, no external deps)
- Add `X-RateLimit-Remaining` and `X-RateLimit-Reset` response headers
- Can upgrade to Redis-backed rate limiting later if needed for multi-instance

**Security considerations:**

- No secrets stored — GitHub manages the token lifecycle
- Token is never stored; only SHA-256 hash used as cache key
- API token auth returns JSON errors (401/403), never redirects
- Cache TTL bounds the window for revoked tokens (5 min default)
- GitHub rate limits on `GET /user` are 5,000/hr per token — caching ensures devpulse stays well under this

**Advantages over custom API keys:**

- Zero key storage — no migration, no `tenant_api_key` table, no hashing at rest
- Zero key management UI — no create/revoke/list UI needed
- Users already know how to manage GitHub PATs
- Token scoping handled by GitHub (fine-grained PATs can be scoped to specific permissions)
- Revocation handled by GitHub — user deletes PAT, access stops (within cache TTL)

### 7. Import Frequency (Daily / Hourly / Hourly + On-demand)

**Effort:** Medium

The importer uses a `SKIP LOCKED` claim queue (`pkg/tenant/import.go`). `PrepareImportQueue` resets completed repos for re-claiming. Currently all tenants are processed every run.

**Changes:**
- `pkg/plan/plan.go` — add `ImportIntervalHours int` and `OnDemandImport bool`
- `tenant_repo` table — add `last_imported_at` timestamp (or reuse `done_at` from claim cycle)
- `PrepareImportQueue` — only reset repos whose `last_imported_at` + interval has elapsed
- Cloud Run — two scheduled jobs (daily + hourly) hitting the same import binary
- On-demand trigger endpoint in `devpulse-site`

**Implementation options:**

1. **Two Cloud Run jobs (recommended):** A daily-schedule job and an hourly-schedule job. Both call the same `devpulse-import` binary. `PrepareImportQueue` checks each repo's plan interval against its last import time, only making eligible repos available for claiming. Free repos only become claimable when 24h have passed; Starter+ repos become claimable every hour.
2. **Single job with plan-aware queue:** One hourly job, but `PrepareImportQueue` filters by plan. Simpler infra but Free tenants still trigger a job start even when no work is available. The two-job approach is cleaner since each job only runs repos at its cadence.

**On-demand import (Pro+):**

Since the import binary already processes from the claim queue, on-demand import means: mark a specific repo as ready-to-claim, then trigger a Cloud Run job execution for that tenant. The site binary would call the Cloud Run Jobs API to launch a one-off execution.

Alternatively, the site binary could invoke the import logic directly (it already has access to the store), but this couples the site to import dependencies and could affect request latency.

---

## Implementation Phases

### Phase 1: Plan Infrastructure + Core Gating

Prerequisites: Stripe integration (`feat/stripe-integration` branch, use placeholder price IDs until Stripe products are created), Starter tier added to `pkg/plan/plan.go`.

Existing Free tier limits drop from 3 repos / 1,000 events to 1 repo / 500 events. No grandfathering — enforced on next import cycle (only one test account affected).

All plans keep the current single hourly Cloud Run import job. Frequency differentiation is deferred to Phase 2.

| Feature | Gate Point | Effort |
|---------|-----------|--------|
| Repos | `pkg/plan/plan.go` constants | Trivial |
| Data Retention | Data API handlers (date clamp) + date picker | Low |
| PDF Export | Template conditional | Low |
| AI (none vs insights vs actions) | Importer + display layer | Low-Medium |
| Reputation (shallow vs deep) | Importer | Low |
| CSV/ZIP Export | New handler + template + retention clamp | Low-Medium |


### Phase 2: Enterprise + Import Frequency

| Feature            | Gate Point                                    | Effort |
|--------------------|-----------------------------------------------|--------|
| API Access         | New auth middleware + GitHub PAT resolution    | Medium |
| Private Repos      | Repo-add validation + GitHub App permissions   | Low-Medium |
| Import Frequency   | `PrepareImportQueue` time check + 2 Cloud Run jobs + Terraform | Medium |
| On-demand Import   | Repo-level claim trigger + Cloud Run Jobs API | Medium |