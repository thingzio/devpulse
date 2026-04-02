# Plan Feature Gating

> Status: **Design** (brainstorming, not yet implemented)
>
> Date: 2026-04-02

This document defines the plan tiers, per-feature gating, and implementation options for devpulse SaaS.

## Plan Matrix

|                          | Free        | Starter ($4.99) | Pro ($9.99)             | Enterprise (Custom)     |
|--------------------------|-------------|-------------------|-------------------------|-------------------------|
| **Repos**                | 1           | 5                 | 25                      | Unlimited               |
| **Data Retention**       | 3 months    | 1 year            | 3 years                 | Unlimited               |
| **Data Export (PDF)**    | -           | Yes               | Yes                     | Yes                     |
| **Data Export (CSV/ZIP)**| -           | -                 | Yes                     | Yes                     |
| **AI**                   | -           | Insights          | Insights + Action Items | Insights + Action Items |
| **Reputation Score**     | Shallow     | Deep              | Deep                    | Deep                    |
| **API Access**           | -           | -                 | Yes                     | Yes                     |
| **Webhook Notifications**| -           | -                 | Future                  | Future                  |
| **Import Frequency**     | Daily       | Hourly            | Hourly + On-demand      | Hourly + On-demand      |

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

**Implementation options:**

1. **Server-side only (recommended):** Compute the earliest allowed date (`NOW() - interval`) in each data handler and pass it as a floor to the store query. Simple, tamper-proof.
2. **Server + client:** Also disable UI date ranges beyond the plan limit for better UX. Slightly more work but avoids confusing "no data" responses.

### 3. Data Export — PDF (Starter+)

**Effort:** Low

PDF generation is client-side via jsPDF in `pkg/server/static/js/app.js:2570`. No server cost.

**Changes:**
- Templates — conditionally render the PDF download button based on plan (hide for Free)
- Frontend JS — check plan before enabling the button (defense in depth)

**Implementation:** Pass `plan` to the template's `pageData`. The template uses a conditional to show/hide the button.

### 4. Data Export — CSV/ZIP (Pro+)

**Effort:** Low-Medium

New server-side export handler. All data is already available via existing store methods.

**Changes:**
- `pkg/server/data.go` — new `csvExportHandler` returning a ZIP archive
- `pkg/server/server.go` — register `GET /data/export` with `scopedWrap`
- Templates — add "Download CSV" button, gated by plan
- `pkg/plan/plan.go` — add `CSVExport bool` to Limits

**Implementation options:**

1. **Single ZIP with multiple CSVs (recommended):** One endpoint, one request. Returns a ZIP containing one CSV per dataset (events, developers, insights time-series, reputation scores, etc.). Uses Go stdlib `encoding/csv` + `archive/zip`. ~150-200 lines.
2. **Per-dataset endpoints:** Separate `/data/export/events.csv`, `/data/export/developers.csv`, etc. More granular but more routes and UI complexity.

**Datasets to include:**
- Event search results (commits, PRs, issues)
- Developer list with reputation scores
- Insight time-series (PR velocity, review latency, retention, etc.)
- Repository metadata and overview metrics

### 5. AI (None / Insights / Insights + Action Items)

**Effort:** Low-Medium

The Claude API call happens in `pkg/importer/importer.go:240` via `data.GenerateInsights()`. The prompt is assembled in `pkg/data/insights_gen.go:177`.

**Changes:**
- `pkg/plan/plan.go` — add `AILevel int` (0=none, 1=insights, 2=insights+actions)
- `pkg/importer/importer.go` — skip `GenerateInsights` call when `AILevel == 0`
- `pkg/data/insights_gen.go` — vary the prompt based on AI level

**Implementation options:**

1. **Prompt variation (recommended):** Pass the AI level to `GenerateInsights`. When `AILevel == 1`, the system prompt instructs the LLM to generate only narrative insights, no action items. When `AILevel == 2`, include action items. This saves tokens for Starter since the response is shorter, and the prompt explicitly omits the action items schema.
2. **Post-filter:** Always generate full insights+actions, strip action items before storing for Starter plans. Simpler code but wastes API tokens — defeats the cost-saving purpose.
3. **Separate prompts:** Two distinct prompt templates. More maintenance but maximum control over token usage per tier.

**Cost impact:** This is the highest-value gate. Free tenants make zero Claude API calls. Starter tenants get shorter responses (fewer output tokens). Pro/Enterprise get the full generation.

### 6. Reputation Score (Shallow / Deep)

**Effort:** Low

Deep reputation is computed in `pkg/importer/importer.go:207` via `store.ImportDeepReputation()`, which calls the GitHub API per-user through the `reputer` library. Shallow reputation uses only locally stored commit/PR counts — no external API calls.

**Changes:**
- `pkg/plan/plan.go` — add `DeepReputation bool`
- `pkg/importer/importer.go` — skip `ImportDeepReputation` call when `DeepReputation == false`
- Frontend — optionally show a "Shallow" badge on the reputation chart for Free users

**Cost impact:** Deep reputation makes multiple GitHub API calls per user (profile, repos, contributions). Gating this for Free saves significant GitHub API quota.

### 7. API Access (Pro+)

**Effort:** Medium

The `/data/*` endpoints already return JSON and are RLS-scoped via `ScopedStoreMiddleware`. They currently require session cookie auth (`RequireAuth` in `pkg/middleware/auth.go`). API access adds an alternative auth path using API keys.

**Auth chain today:**
```
Request → RequireAuth (cookie → ValidateSession → tenant) → ScopedStoreMiddleware (conn → set_config → scoped Store) → handler
```

**Auth chain with API keys:**
```
Request → RequireAuthOrAPIKey (cookie OR Bearer token → tenant) → ScopedStoreMiddleware (unchanged) → handler
```

The key insight is that `ScopedStoreMiddleware` only needs a `*tenant.Tenant` in context — it doesn't care how the tenant was resolved. So the only new code is an alternative auth path that resolves tenant from an API key instead of a session cookie.

**Data model:**

New `tenant_api_key` table:

```sql
CREATE TABLE tenant_api_key (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id  UUID NOT NULL REFERENCES tenant(id) ON DELETE CASCADE,
    name       TEXT NOT NULL,              -- user-provided label ("CI key", "Grafana")
    key_prefix TEXT NOT NULL,              -- first 8 chars, for display ("dp_a1b2...")
    key_hash   TEXT NOT NULL,              -- SHA-256 of the full key
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used_at TIMESTAMPTZ,
    revoked_at TIMESTAMPTZ                -- soft-delete, NULL = active
);

-- RLS: tenant can only see/manage their own keys
ALTER TABLE tenant_api_key ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_api_key_policy ON tenant_api_key
    USING (tenant_id = current_setting('app.tenant_id')::uuid);
```

**Key format:** `dp_<32 random hex chars>` (prefix makes keys greppable in logs, 128 bits of entropy).

**Changes:**

- `pkg/data/postgres/sql/migrations_saas/` — new migration for `tenant_api_key` table
- `pkg/tenant/apikey.go` — CRUD: `CreateAPIKey`, `ListAPIKeys`, `RevokeAPIKey`, `ValidateAPIKey`
  - `CreateAPIKey` generates key, stores SHA-256 hash, returns plaintext once
  - `ValidateAPIKey` hashes the provided key, looks up by hash where `revoked_at IS NULL`, updates `last_used_at`
- `pkg/middleware/auth.go` — extend `RequireAuth` (or add `RequireAuthOrAPIKey`) to check `Authorization: Bearer dp_...` header before falling back to cookie. On match, call `ValidateAPIKey`, inject tenant into context. On API key auth, return 401 JSON instead of redirect.
- `pkg/server/server.go` — swap `RequireAuth` for `RequireAuthOrAPIKey` on the data route group (no route changes needed)
- `pkg/server/settings.go` — new handlers for API key management UI (list, create, revoke)
- Templates — settings page section for API keys (name, prefix, created, last used, revoke button)
- `pkg/plan/plan.go` — add `APIAccess bool` to `Limits`
- Plan gate — `RequireAuthOrAPIKey` checks plan before accepting API key auth; returns 403 if plan doesn't allow it

**Implementation options:**

1. **Extend existing auth middleware (recommended):** Modify `RequireAuth` to first check for `Authorization: Bearer dp_...` header. If present, validate API key and inject tenant. If not present, fall back to cookie auth as today. `ScopedStoreMiddleware` remains unchanged. This means all 30+ `/data/*` endpoints get API access for free — no route duplication. ~200-250 lines of new Go code.
2. **Separate `/api/v1/` prefix:** Mount a second route group with its own middleware stack. Cleaner separation and allows independent versioning, but duplicates route registration and requires maintaining two sets of paths. Only justified if the API needs different response shapes than the dashboard.

**Rate limiting:**

- Start simple: per-tenant limit (e.g., 100 req/min for Pro, configurable for Enterprise)
- Implement with an in-memory token bucket per tenant ID (stdlib, no external deps)
- Add `X-RateLimit-Remaining` and `X-RateLimit-Reset` response headers
- Can upgrade to Redis-backed rate limiting later if needed for multi-instance

**Security considerations:**

- Keys are hashed (SHA-256) at rest — plaintext shown only once at creation
- `key_prefix` stored separately for display without exposing the full key
- API key auth returns JSON errors (401/403), never redirects
- `last_used_at` tracking enables stale key detection
- Soft-delete via `revoked_at` preserves audit trail
- RLS policy on `tenant_api_key` table ensures tenants can only manage their own keys

**UI:**

Settings page gains an "API Keys" section:
- Table: name, prefix (`dp_a1b2...`), created date, last used date, revoke button
- "Create Key" form: name input, submit → modal showing the full key once with copy button
- Plan gate: section hidden or shows upgrade prompt for Free/Starter tenants

### 8. Import Frequency (Daily / Hourly / Hourly + On-demand)

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

### Phase 1: Plan Infrastructure + Low-Effort Gates

Prerequisites: Stripe integration (feature branch pending), Starter tier added.

| Feature | Gate Point | Effort |
|---------|-----------|--------|
| Repos | `pkg/plan/plan.go` constants | Trivial |
| Data Retention | Data API handlers (date clamp) | Low |
| PDF Export | Template conditional | Low |
| AI (none vs insights vs actions) | Importer + prompt | Low-Medium |
| Reputation (shallow vs deep) | Importer | Low |

### Phase 2: Export + Import Frequency

| Feature | Gate Point | Effort |
|---------|-----------|--------|
| CSV/ZIP Export | New handler + template | Low-Medium |
| Import Frequency | `PrepareImportQueue` + Cloud Run jobs | Medium |

### Phase 3: API Access

| Feature | Gate Point | Effort |
|---------|-----------|--------|
| API Access | New auth middleware + key management | Medium |

### Future (unscheduled)

| Feature | Gate Point | Effort |
|---------|-----------|--------|
| Webhook Notifications | New table + dispatch + UI | High |

---

## Open Questions

1. ~~**Downgrade behavior:**~~ Resolved: downgrade takes effect immediately — data range, features, and limits switch to the new plan with no grace period.
3. **AI action items format:** Are action items stored separately from insights today, or embedded in the same text blob? This affects how cleanly we can split the two tiers.
4. **On-demand import throttle:** Should on-demand imports have a cooldown (e.g., max 1 per hour per repo) to prevent abuse?
5. **CSV export scope:** Export current repo only, or all repos for the tenant in one ZIP?
6. **Events-per-week limits:** The current `MaxEventsPerWeek` limits (1000/15000) need re-evaluation against the new repo counts (1/5/25). Should they scale proportionally?
7. **API key limits per tenant:** How many API keys can a Pro tenant create? Suggest 5 for Pro, unlimited for Enterprise.
8. **API rate limits:** 100 req/min for Pro is a starting point — should Enterprise get a higher or configurable limit?
