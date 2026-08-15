# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project

`devpulse` is a multi-tenant SaaS for GitHub project health analytics. Two binaries: `devpulse-site` (HTTP server + integrated admin dashboard), `devpulse-import` (batch worker). PostgreSQL with Row-Level Security for tenant isolation. Deployed to Cloud Run.

## Build & Test

```shell
make test          # unit tests with race detector
make lint          # go vet + golangci-lint + yamllint + tfsec
make qualify       # test-coverage + lint + govulncheck + e2e
make build         # goreleaser single-target build
make server        # run dev server with --debug
```

Tool versions and quality thresholds are centralized in `.settings.yaml` (single source of truth). Go version is pinned in `go.mod` (currently 1.26). The `.golangci.yaml` at the repo root configures linting.

## Non-Negotiable Rules

1. **Read before writing** — Never modify code you haven't read
2. **Tests must pass** — `make test` with race detector; never skip or disable tests
3. **Run `make qualify` often** — Run at every stopping point (after completing a phase, before commits). Fix ALL lint/test failures before proceeding
4. **Use project patterns** — Learn existing code before inventing new approaches
5. **3-strike rule** — After 3 failed fix attempts, stop and reassess

## Git Configuration

- Commit to `main` branch (not `master`)
- Do use `-S` to cryptographically sign the commit
- Do NOT add `Co-Authored-By` lines (organization policy)
- Do not sign-off commits (no `-s` flag) unless the commit can't be cryptographically signed

## Code Conventions

**Context propagation (CRITICAL):**
- Every Store interface method takes `ctx context.Context` as its first parameter
- Every DB call must use `*Context` variants: `ExecContext`, `QueryContext`, `QueryRowContext`, `PrepareContext`, `BeginTx`
- Never use `context.Background()` in production code — always thread the caller's ctx
- HTTP handlers pass `r.Context()` to all Store/tenant calls
- Import worker passes its `ctx` through all layers
- When adding a new Store method: add to interface in `pkg/data/store.go`, implement in `pkg/data/postgres/`, update all callers

**Tenant isolation (CRITICAL):**
- Data API routes use `ScopedStoreMiddleware` which acquires a dedicated `db.Conn()` and calls `set_config('app.tenant_id', $1, false)` for RLS
- Data handlers use `storeFromRequest(r, defaultStore)` to get the tenant-scoped Store from context
- The `DBTX` interface is context-only — both `*sql.DB` and `*sql.Conn` satisfy it natively
- `NewFromConn(conn)` creates a Store from a dedicated connection (no adapter needed)
- When adding a new data endpoint: use `scopedWrap()` in the router, use `storeFromRequest()` in the handler

**Tenant schema changes:**
- When adding a column to the `tenant` table, you MUST update ALL of these:
  - `upsertTenantSQL`, `getTenantByGitHubIDSQL`, `getTenantByIDSQL` in `pkg/tenant/tenant.go`
  - `validateSessionSQL` in `pkg/tenant/session.go` (auth loop if missed!)
  - `scanTenant()` field list in `pkg/tenant/tenant.go`
  - `Tenant` struct in `pkg/tenant/tenant.go`
  - Migration in `pkg/data/postgres/sql/migrations_saas/001_initial.sql`

**Schema migrations (CRITICAL):**
- There are **two independent chains**, each with its own version table and advisory lock:

  | Chain | Directory | Version table | Prod high-water mark |
  |---|---|---|---|
  | base | `sql/migrations/` | `schema_version` | **14** |
  | saas | `sql/migrations_saas/` | `saas_schema_version` | **25** |

- Both `001_initial.sql` files are squashed. **The directory listing does NOT tell you the high-water mark** — the base chain contains only `001_initial.sql` but its version table holds `1,2,3,4,12,13,14` from the migrations that were squashed into it. Verify with `SELECT MAX(version) FROM schema_version` before numbering anything
- New migrations MUST be numbered above that mark. `applyMigrations` silently skips any file where `version <= MAX(version)` — a mis-numbered migration does not error, it just never runs, and code that assumes the new schema ships anyway
- A fresh test container starts at version 0 and applies everything, so **CI cannot catch a mis-numbered migration**. `migrate_legacy_test.go` reconstructs production's version history to cover that gap — extend it when adding migrations
- Never rely on editing `001_initial.sql` for production schema changes — it only affects fresh databases
- Always use `IF NOT EXISTS` / `IF EXISTS` or an `information_schema` guard for idempotent DDL, so the same file is safe against both legacy and fresh databases

**Error handling:**
- Use `fmt.Errorf("context: %w", err)` for wrapping — never bare `return err`
- Exported sentinel errors: `ErrRepoLimitExceeded`, `ErrSessionInvalid`
- Exported sentinel errors: `ErrDBNotInitialized` (cross-package)
- Unexported sentinel errors: `errNoToken`

**Logging:**
- JSON-only via `log/slog` (Info, Debug, Warn, Error) — never `fmt.Println`
- `DEVPULSE_DEBUG=true` for debug level

**Database:**
- All SQL constants defined at the top of the file they're used in
- COALESCE pattern for optional filters: `WHERE col = COALESCE(?, col)`
- Transactions with explicit rollback on error
- Upserts via `INSERT ... ON CONFLICT(...) DO UPDATE SET`
- Limit checks inside transactions to prevent TOCTOU races

**HTTP handlers:**
- Return `http.HandlerFunc` closures: `func handler(db *sql.DB) http.HandlerFunc`
- Use `writeJSON(w, status, v)` and `writeError(w, status, msg)` helpers
- Use `storeFromRequest(r, store)` in data API handlers (gets RLS-scoped Store)
- Generic insight handlers: `insightWithEntityHandler(store, label, fn)` and `insightHandler(store, label, fn)`
- All outbound HTTP calls use a `*http.Client` with timeout (10-30s), never `http.DefaultClient`

**Imports:**
- GitHub API via `github.com/google/go-github/v83/github`
- Testing via `github.com/stretchr/testify` (assert + require)
- PostgreSQL via `github.com/lib/pq`
- JWT via `github.com/golang-jwt/jwt/v5`

**Testing:**
- `setupTestDB(t)` helper creates temp Postgres container with all migrations
- All test functions must create `ctx := context.Background()` and pass to Store methods
- Table-driven tests where applicable
- Test both nil DB and empty DB cases for query functions

## Anti-Patterns (Do Not Do)

| Anti-Pattern | Correct Approach |
|---|---|
| Modify code without reading it first | Always `Read` files before `Edit` |
| Skip or disable tests to make CI pass | Fix the actual issue |
| Invent new patterns | Study existing code in same package first |
| Use `fmt.Println` for logging | Use `slog.Info/Debug/Warn/Error` |
| Use `context.Background()` in handlers | Thread `r.Context()` or caller's `ctx` |
| Use `http.DefaultClient` for outbound calls | Use a `*http.Client` with timeout |
| Use `s.db.Exec()` (non-context) | Use `s.db.ExecContext(ctx, ...)` |
| Bare `return err` without wrapping | Use `fmt.Errorf("context: %w", err)` |
| Use `fmt.Sprintf` for SQL with user input | Use parameterized queries (`$1, $2`) |
| Add tenant column without updating all SQL | Update ALL 5 SQL constants + scanTenant + struct |
| Add data endpoint without `scopedWrap()` | Data routes must use RLS scoping middleware |
| Add features not requested | Implement exactly what was asked |
| Create new files when editing suffices | Prefer `Edit` over `Write` |
| Guess at missing parameters | Ask for clarification |
| Continue after 3 failed fix attempts | Stop, reassess approach, explain blockers |

## Design Principles

- Partial failure is the steady state — design for timeouts, bounded retries
- Boring first — default to proven, simple technologies
- Observability is mandatory — structured logging
- Correctness must be reproducible — same inputs, same outputs
- Trust requires verifiable provenance — container images built in CI, govulncheck, dependency pinning

## Decision Framework

When choosing between approaches, prioritize in this order:
1. **Testability** — Can it be unit tested without external dependencies?
2. **Readability** — Can another engineer understand it quickly?
3. **Consistency** — Does it match existing patterns in the codebase?
4. **Simplicity** — Is it the simplest solution that works?
5. **Reversibility** — Can it be easily changed later?

## Architecture

```
cmd/devpulse-site/     HTTP server entrypoint (dashboard, OAuth, webhooks, data API)
cmd/devpulse-import/   Batch import worker entrypoint (events, reputation, insights)
pkg/middleware/admin.go RequireAdmin middleware (session auth + DEVPULSE_ADMIN_USERS whitelist)
pkg/server/             HTTP server, handlers, scoped.go (RLS middleware), data.go (chart API)
pkg/server/static/      Frontend: CSS, JS, images (embedded via go:embed)
pkg/server/templates/   HTML templates: header, home, footer, landing, tos, help
pkg/importer/           Tenant import worker with weekly event limit check + LLM insights
pkg/data/               Store interface (all methods take ctx), shared types, helpers
pkg/data/postgres/      PostgreSQL Store (DBTX interface: *sql.DB and *sql.Conn)
pkg/data/ghutil/        Shared GitHub API helpers (rate limiting, user mapping)
pkg/data/insights_gen.go  LLM insights generation via Anthropic API
pkg/tenant/             Tenant CRUD, sessions, GitHub App JWT, installations, overview
pkg/middleware/         Auth (session cookie), tenant scope (dedicated conn + set_config)
pkg/oauth/              GitHub OAuth web flow
pkg/net/                HTTP client utilities
infra/saas/             Terraform for GCP infrastructure
tools/                  Dev scripts (version bump, shared helpers)
```

Two binaries with independent lifecycles. `devpulse-site` serves HTTP and includes an integrated admin dashboard at `/admin` (session auth + username whitelist). `devpulse-import` runs batch imports. Each creates its own store via `postgres.NewFromEnv()`.

Data flow: GitHub App webhook → tenant_repo → scheduled import worker → PostgreSQL (RLS-scoped) → dashboard

Tenant isolation layers:
1. **RLS policies** on all data tables, filtering by `app.tenant_id` session variable
2. **ScopedStoreMiddleware** acquires dedicated `db.Conn()`, calls `set_config`, creates scoped Store
3. **Data handlers** use `storeFromRequest()` to get the scoped Store from request context
4. **Repo-add gating** — public repo check (HEAD to GitHub API) + GitHub App installation check (tenant must have at least one active installation for import tokens)
5. **Sample repos** — `devpulse_tenant_repo.sample = TRUE` rows are seeded on first login via `SAMPLE_REPOS` env var. Excluded from repo count and weekly event quota limits. Shared data (events already imported by other tenants) means new users see analytics immediately

## Environment Variables

- `DATABASE_URL` — PostgreSQL connection URI (required)
- `GITHUB_OAUTH_CLIENT_ID` — GitHub OAuth App client ID
- `GITHUB_OAUTH_CLIENT_SECRET` — GitHub OAuth App client secret
- `GITHUB_WEBHOOK_SECRET` — GitHub App webhook HMAC secret
- `GITHUB_APP_ID` — GitHub App ID for installation tokens
- `GITHUB_APP_KEY_PATH` — path to GitHub App private key PEM
- `BASE_URL` — public base URL (e.g. https://devpulse.thingz.io)
- `ANTHROPIC_API_KEY` — optional, enables LLM insights generation
- `ANTHROPIC_MODEL` — optional, defaults to `claude-haiku-4-5-20251001`
- `BACKFILL_MAX_DAYS` — max age in days for PR size backfill (default: 90)
- `IMPORT_FRESH_DAYS` — fresh pass lookback window in days (default: 21)
- `IMPORT_BACKFILL_CHUNK_DAYS` — backfill chunk size in days (default: 7)
- `IMPORT_DB_BATCH_SIZE` — events per DB transaction during flush (default: 100)
- `SAMPLE_REPOS` — comma-separated `org/repo` pairs seeded for new users (e.g. `etcd-io/etcd,prometheus/prometheus`); sample repos don't count against plan limits

## CI/CD

GitHub Actions workflows in `.github/workflows/`:

| Workflow | Trigger | Purpose |
|----------|---------|---------|
| `test-on-push.yaml` | push to main, PRs (paths-ignore: docs, md, .claude) | Calls reusable test workflow |
| `test-on-call.yaml` | reusable (workflow_call) | lint, unit test, integration test, e2e |
| `tfsec-on-push.yaml` | push to main, PRs (paths: infra/**, .settings.yaml) | Terraform security scanning |
| `release-on-tag.yaml` | version tags (`v*.*.*`) | goreleaser build, container image push, Cloud Run deploy |
| `deploy-saas.yaml` | manual (workflow_dispatch) | Deploy devpulse to Cloud Run |

## Release Process

Releases are triggered by version tags. Use `make bump-patch`, `make bump-minor`, or `make bump-major` to tag and push.

- **Build**: goreleaser v2 compiles linux/amd64+arm64, ko builds container images
- **Images**: `devpulse-site`, `devpulse-import` pushed to Artifact Registry (`devpulse-saas-images`)
- **Deploy**: Cloud Run service + job (import) + admin updated via `deploy-saas.yaml` or `release-on-tag.yaml`

## Changelog Updates

When asked to "update changelog" (or similar):

1. Read `pkg/server/templates/changelog.html` to see current entries
2. Review git log since the last changelog date: `git log --oneline --since="YYYY-MM-DD"`
3. Summarize **user-visible and user-impacting changes only** — skip internal refactors, lint fixes, CI changes, or implementation details that don't affect the user experience
4. Add a new entry at the top with **today's date**, using the same HTML structure as existing entries
5. Remove the oldest (last) entry to keep the list from growing indefinitely
6. Commit with message: `feat: update changelog with <date> entries`

**Entry format** (matches existing pattern):
```html
<div class="landing-feature" style="margin-bottom:20px">
  <h3>Month Day, Year</h3>
  <ul>
    <li>User-visible change description</li>
  </ul>
</div>
```

**Rules:**
- One entry per day regardless of how many commits span multiple days
- **Only include changes visible to end users of the web app** — new UI features, behavior changes, bug fixes they'd notice
- Do NOT include: admin/operator tooling (CLI scripts, daily reports, Terraform), internal refactors, infra changes, CI/CD, API-only changes, or implementation details
- The test is: "Would a user signing into devpulse.thingz.io notice this?" — if no, skip it
- Link to relevant pages where applicable (e.g. `<a href="/help">Help page</a>`)
- Keep descriptions concise — one sentence per bullet
