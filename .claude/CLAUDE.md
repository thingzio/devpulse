# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project

`devpulse` is a multi-tenant SaaS for GitHub project health analytics. Single Go binary with `serve` (HTTP server) and `import` (scheduled worker) subcommands. PostgreSQL with Row-Level Security for tenant isolation. Deployed to Cloud Run.

## Build & Test

```shell
make test          # unit tests with race detector
make lint          # go vet + golangci-lint
make qualify       # test + lint + govulncheck vulnerability scan
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

**Error handling:**
- Use `fmt.Errorf("context: %w", err)` for wrapping — this is the project-wide pattern
- Sentinel errors defined as package-level vars (e.g. `errDBNotInitialized`)

**Logging:**
- Use `log/slog` (Info, Debug, Warn, Error) — never `fmt.Println`

**Database:**
- All SQL constants defined at the top of the file they're used in
- COALESCE pattern for optional filters on non-nullable columns: `WHERE col = COALESCE(?, col)`
- IFNULL/COALESCE pattern for nullable columns (e.g. entity): `IFNULL(d.entity, '') = COALESCE(?, IFNULL(d.entity, ''))`
- Developer upsert preserves existing entity when new value is empty: `CASE WHEN ? = '' THEN COALESCE(developer.entity, '') ELSE ? END`
- Transactions with explicit rollback on error
- Upserts via `INSERT ... ON CONFLICT(...) DO UPDATE SET`

**HTTP handlers:**
- Return `http.HandlerFunc` closures: `func handler(db *sql.DB) http.HandlerFunc`
- Use `writeJSON(w, status, v)` and `writeError(w, status, msg)` helpers
- Query params: `r.URL.Query().Get("key")`, convert with `queryParamInt()`

**Imports:**
- GitHub API via `github.com/google/go-github/v83/github`
- CLI via `github.com/urfave/cli/v3`
- Testing via `github.com/stretchr/testify` (assert + require)
- PostgreSQL via `github.com/lib/pq`
- JWT via `github.com/golang-jwt/jwt/v5`

**Testing:**
- `setupTestDB(t)` helper creates temp DB with all migrations
- Table-driven tests where applicable
- Test both nil DB and empty DB cases for query functions

## Anti-Patterns (Do Not Do)

| Anti-Pattern | Correct Approach |
|---|---|
| Modify code without reading it first | Always `Read` files before `Edit` |
| Skip or disable tests to make CI pass | Fix the actual issue |
| Invent new patterns | Study existing code in same package first |
| Use `fmt.Println` for logging | Use `slog.Info/Debug/Warn/Error` |
| Add features not requested | Implement exactly what was asked |
| Create new files when editing suffices | Prefer `Edit` over `Write` |
| Guess at missing parameters | Ask for clarification |
| Continue after 3 failed fix attempts | Stop, reassess approach, explain blockers |

## Design Principles

- Partial failure is the steady state — design for timeouts, bounded retries
- Boring first — default to proven, simple technologies
- Observability is mandatory — structured logging
- Correctness must be reproducible — same inputs, same outputs
- Trust requires verifiable provenance — SBOM, Sigstore, GitHub attestations

## Decision Framework

When choosing between approaches, prioritize in this order:
1. **Testability** — Can it be unit tested without external dependencies?
2. **Readability** — Can another engineer understand it quickly?
3. **Consistency** — Does it match existing patterns in the codebase?
4. **Simplicity** — Is it the simplest solution that works?
5. **Reversibility** — Can it be easily changed later?

## Architecture

```
cmd/devpulse/           Entrypoint (serve, import subcommands)
pkg/data/               Store interface, shared types, helpers
pkg/data/postgres/      PostgreSQL Store implementation + migrations (base + SaaS)
pkg/data/ghutil/        Shared GitHub API helpers (rate limiting, user mapping)
pkg/data/insights_gen.go  LLM insights generation
pkg/oauth/              GitHub OAuth web flow
pkg/tenant/             Tenant CRUD, sessions, GitHub App, installations
pkg/middleware/         Auth middleware, tenant scope injection (RLS)
pkg/net/                HTTP client utilities
infra/saas/             Terraform for GCP infrastructure
tools/                  Dev scripts (version bump, shared helpers)
```

Subcommands: `serve` (HTTP server with OAuth, webhooks, dashboard), `import` (scheduled tenant data import worker)

Data flow: GitHub App webhook → tenant_repo → scheduled import worker → PostgreSQL (RLS-scoped) → dashboard

Tenant isolation: PostgreSQL Row-Level Security (RLS) policies filter data via `app.tenant_id` session variable set by middleware. Global tables (event, developer, etc.) join through `tenant_repo`; tenant-scoped tables have direct `tenant_id` columns.

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

## CI/CD

GitHub Actions workflows in `.github/workflows/`:

| Workflow | Trigger | Purpose |
|----------|---------|---------|
| `test-on-push.yaml` | push to main, PRs | Calls reusable test workflow |
| `test-on-call.yaml` | reusable (workflow_call) | tidy, lint, test with race detector |
| `release-on-tag.yaml` | version tags (`v*.*.*`) | goreleaser build, container image push, Cloud Run deploy |
| `deploy-saas.yaml` | manual (workflow_dispatch) | Deploy devpulse to Cloud Run |
| `codeql-analysis.yml` | schedule, push | CodeQL security analysis (Go + JavaScript) |

## Release Process

Releases are triggered by version tags. Use `make bump-patch`, `make bump-minor`, or `make bump-major` to tag and push.

- **Build**: goreleaser v2 compiles linux/amd64+arm64, ko builds container images
- **Images**: pushed to `ghcr.io/thingzio/devpulse` and `ghcr.io/thingzio/devpulse-cloud`
- **Deploy**: Cloud Run service + job updated via `deploy-saas.yaml` or `release-on-tag.yaml`
