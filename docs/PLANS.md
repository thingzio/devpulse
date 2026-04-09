# Plan Feature Gating — Phase 2

> **Phase 1 (shipped):** Repos, data retention, exports (PDF/CSV), AI insights, deep reputation. See `pkg/plan/plan.go` for current limits.
>
> **Retention limits:** Free=3 months, Starter=6 months, Pro=12 months, Enterprise=unlimited.
>
> **Chart granularity:** Charts auto-select weekly granularity for time ranges ≤6 months and monthly for longer ranges.

This document covers remaining plan features not yet implemented.

## Plan Matrix (Phase 2 columns)

|                          | Free        | Starter     | Pro         | Enterprise          |
|--------------------------|-------------|-------------|-------------|---------------------|
| **Private Repos**        | -           | -           | -           | Yes                 |
| **API Access**           | -           | -           | -           | Yes                 |
| **Import Frequency**     | Daily       | Hourly      | Hourly      | Hourly + On-demand  |
| **Dedicated Instance**   | -           | -           | -           | Optional (quoted)   |

---

## 1. API Access (Enterprise)

**Effort:** Medium

The `/data/*` endpoints already return JSON and are RLS-scoped via `ScopedStoreMiddleware`. They currently require session cookie auth. API access adds an alternative auth path using GitHub Personal Access Tokens (PATs) — no custom key storage needed.

**Auth chain with GitHub PAT:**
```
Request → RequireAuthOrToken (cookie OR Bearer <github PAT> → resolve GitHub user → tenant) → ScopedStoreMiddleware (unchanged) → handler
```

`ScopedStoreMiddleware` only needs a `*tenant.Tenant` in context — it doesn't care how the tenant was resolved. By using GitHub PATs, devpulse delegates token lifecycle entirely to GitHub.

**How it works:**

1. User creates a GitHub PAT (classic or fine-grained) — only `read:user` scope required
2. API request includes `Authorization: Bearer ghp_...`
3. Middleware calls GitHub `GET /user` with the token → gets GitHub user ID
4. Look up tenant via `getTenantByGitHubIDSQL` (already exists)
5. Check tenant plan allows API access → inject tenant into context
6. `ScopedStoreMiddleware` handles RLS as usual

**Token resolution caching:** Cache `token_hash → (github_id, resolved_at)` in-memory with 5-minute TTL. First request hits GitHub API; subsequent requests use cache. Revoked tokens stop working within TTL window.

**Changes:**
- `pkg/middleware/auth.go` — extend `RequireAuth` to check `Authorization: Bearer ghp_...` header first, fall back to cookie
- `pkg/middleware/token_cache.go` — in-memory token resolution cache (~50-80 lines)
- `pkg/server/server.go` — swap `RequireAuth` for `RequireAuthOrToken` on data routes
- `pkg/plan/plan.go` — add `APIAccess bool` to `Limits`

**Rate limiting:** Per-tenant limit (e.g., 100 req/min), in-memory token bucket, `X-RateLimit-Remaining` / `X-RateLimit-Reset` headers.

**Advantages over custom API keys:** Zero key storage, zero key management UI, users already know GitHub PATs, revocation handled by GitHub.

## 2. Private Repos (Enterprise)

**Effort:** Low-Medium

Currently repo-add requires a public repo (HEAD request to GitHub API). Private repo support requires:
- Remove the public repo check for Enterprise tenants
- GitHub App must have `Contents: Read-only` permission on the private repo's org
- Installation token scoping already handles access — if the app is installed on the org, it can read private repos

**Changes:**
- `pkg/plan/plan.go` — add `PrivateRepos bool` to `Limits`
- `pkg/server/repo.go` — skip public check when tenant plan allows private repos
- Verify GitHub App permissions cover private repo metadata access

## 3. Import Frequency (Daily / Hourly / Hourly + On-demand)

**Effort:** Medium

The importer uses deterministic task-index sharding. Each Cloud Run task gets a disjoint slice of repos. Repos shared by multiple tenants are imported once and marked done for all.

**Changes:**
- `pkg/plan/plan.go` — add `ImportIntervalHours int` and `OnDemandImport bool`
- Sharding already ensures each repo is processed at most once per cycle
- Two Cloud Run jobs: daily-schedule + hourly-schedule, same binary
- Free repos only claimable when 24h have passed; Starter+ repos claimable every hour

**On-demand import (Enterprise):** Mark a specific repo as ready-to-claim, trigger a Cloud Run job execution via the Cloud Run Jobs API from the site binary.

---

## Implementation Order

| Feature | Gate Point | Effort |
|---------|-----------|--------|
| API Access | Auth middleware + GitHub PAT resolution | Medium |
| Private Repos | Repo-add validation + GitHub App permissions | Low-Medium |
| Import Frequency | Single scheduled job every 2 hours with sharded tasks | Medium |
| On-demand Import | Repo-level claim trigger + Cloud Run Jobs API | Medium |
