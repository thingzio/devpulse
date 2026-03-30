# Architecture

Multi-tenant SaaS for GitHub project health analytics. Two binaries: `devpulse-site` (HTTP server) and `devpulse-import` (batch worker). PostgreSQL with Row-Level Security for tenant isolation.

## Data Flow

```
GitHub App webhook ──→ devpulse (serve) ──→ tenant_repo ──→ PostgreSQL (RLS-scoped)
Cloud Scheduler ──→ devpulse (import) ──→ per-tenant install tokens ──→ PostgreSQL
Browser ──→ devpulse (serve) ──→ OAuth ──→ RLS-scoped dashboard
Browser ──→ POST /api/repos ──→ public check + install check ──→ tenant_repo
```

### Repo-Add Gating

Adding a repo requires two conditions:
1. **Public repo** — verified via HEAD request to GitHub API
2. **Active GitHub App installation** — tenant must have at least one installation (org-specific preferred, falls back to any). This ensures the import job can mint installation tokens for authenticated API access (12,500 req/hr).

The GitHub App installation is separate from OAuth login. Users must install the DevPulseThingz app on their org/account after signing in. The webhook handler records installations in `github_app_installation`.

## Directory Structure

```
devpulse/
├── cmd/devpulse-site/     HTTP server entrypoint (dashboard, OAuth, webhooks, data API)
├── cmd/devpulse-import/   Batch import worker entrypoint
├── pkg/
│   ├── server/             HTTP server, handlers, templates, static assets
│   │   ├── static/         Frontend: CSS, JS, images (embedded via go:embed)
│   │   └── templates/      HTML templates: header, home, footer, landing, tos
│   ├── importer/           Tenant import worker
│   ├── data/               Store interface, shared types, helpers
│   │   ├── postgres/       PostgreSQL Store implementation + migrations
│   │   └── ghutil/         Shared GitHub API helpers (rate limiting, user mapping)
│   ├── tenant/             Tenant CRUD, sessions, GitHub App JWT, installations
│   ├── middleware/         Auth middleware, tenant scope (RLS via dedicated conn)
│   ├── oauth/              GitHub OAuth web flow
│   ├── logging/            JSON structured logging setup
│   └── net/                HTTP client utilities with rate limit handling
├── infra/saas/             Terraform for GCP infrastructure
├── tools/                  Dev scripts (version bump, shared helpers)
├── docs/                   Documentation
├── .github/                CI/CD workflows, composite actions, CODEOWNERS
└── .settings.yaml          Centralized tool versions and quality thresholds
```

## Mode Selection

Two separate binaries with independent lifecycles. `devpulse-site` serves HTTP, `devpulse-import` runs batch imports. Each creates its own store via `postgres.NewFromEnv()`.

## Data Layer

**PostgreSQL only** — via [`github.com/lib/pq`](https://pkg.go.dev/github.com/lib/pq). Connection URI via `DATABASE_URL` env var. Migrations run automatically on startup.

### Key Tables

| Table | Purpose |
|-------|---------|
| `event` | Contribution events (PRs, reviews, issues, comments, forks) |
| `developer` | Developer profiles, entity affiliations, reputation scores |
| `repo_meta` | Repository metadata (stars, forks, language, license, community profile) |
| `repo_metric_history` | Daily star/fork counts for trend charts |
| `release` | Release tags, dates |
| `release_asset` | Per-asset download counts |
| `container_version` | Container image versions |
| `state` | Import pagination state for incremental fetches |
| `sub` | Entity name substitution rules |
| `repo_insights` | LLM-generated observations per repo |
| `tenant` | Registered tenants (GitHub identity, plan, ToS) |
| `tenant_repo` | Repos tracked per tenant |
| `tenant_member` | Multi-user tenant membership |
| `session` | Server-side sessions (SHA-256 hashed tokens) |
| `github_app_installation` | GitHub App installations per tenant |

### Query Patterns

- **Optional filters**: `WHERE col = COALESCE(?, col)`
- **Upserts**: `INSERT ... ON CONFLICT(...) DO UPDATE SET`
- **Transactions**: Explicit `BEGIN`/`COMMIT` with rollback on error
- **Repo limit**: Count check inside transaction to prevent races

### Migrations

Two migration sets, both using `applyMigrations()` in `pkg/data/postgres/migrate.go`:
- `sql/migrations/001_initial.sql` — base tables (schema_version, advisory lock 1)
- `sql/migrations_saas/001_initial.sql` — tenant tables + RLS policies (saas_schema_version, advisory lock 2)

## Tenant Isolation

PostgreSQL Row-Level Security (RLS) policies filter data per tenant:
- Middleware acquires a dedicated `db.Conn()` per request
- Sets `app.tenant_id` via parameterized `set_config($1, false)`
- Global tables (event, developer, etc.) filtered via JOIN to `tenant_repo`
- Tenant-scoped tables filtered by direct `tenant_id` column

## Import Pipeline

The import worker (`pkg/importer/`) iterates active tenants and runs per-repo:

1. **Metadata** — repo stars, forks, language, license (skips if fresh < 24h)
2. **Events** — PRs, reviews, issues, comments, forks (incremental via pagination state)
3. **Releases** — tags, dates, asset downloads
4. **Metric history** — daily star/fork counts
5. **Container versions** — image version tracking
6. **Reputation** — shallow scores from local data

Token resolution: `GITHUB_TOKEN` env var (fallback) or GitHub App installation tokens (planned).

## Dashboard

### Server

The HTTP server (`pkg/server/server.go`) serves:
- **Static assets** — CSS, JS, images via `go:embed` filesystem
- **HTML templates** — Go `html/template` with header/home/footer structure
- **Data API** — 30+ JSON endpoints under `/data/` for chart data (all authenticated)
- **Tenant API** — `/api/repos/*` for repo management (overview, add, remove, search)

### Frontend

- **jQuery 3.6** — DOM manipulation, AJAX calls
- **Chart.js 4.4** — all chart rendering (bar, line, pie, polar area, mixed)
- **No build step** — vanilla JS + CSS, no bundler or framework

### Layout

The dashboard is organized into:
1. **Top bar** — search input (`org:` / `repo:` prefix), period selector, theme toggle
2. **Summary banner** — global counts (orgs, repos, events, contributors, last import)
3. **Seven tabs** — Health, Activity, Velocity, Quality, Community, Events, Insights
4. **Repository Overview** — table with add/remove, search autocomplete via GitHub API

Charts load lazily per tab. Tab state persists in the URL hash.

## Authentication

GitHub OAuth web flow (`pkg/oauth/`):
1. User clicks "Sign in with GitHub"
2. Redirect to GitHub OAuth authorize endpoint
3. Callback exchanges code for token, fetches user profile
4. OAuth token discarded immediately — not stored
5. Tenant upserted, session created (SHA-256 hashed, stored in DB)
6. Session cookie set (`__Host-session` on HTTPS, `session` on HTTP)

## Rate Limit Handling

All GitHub API calls go through `pkg/net/` which:
- Checks `X-RateLimit-Remaining` headers after each response
- Waits with jitter backoff when approaching the limit
- Logs rate limit state at debug level

## CI/CD

GitHub Actions workflows in `.github/workflows/`:

| Workflow | Trigger | Purpose |
|----------|---------|---------|
| `test-on-push.yaml` | push to main, PRs | Calls reusable test workflow |
| `test-on-call.yaml` | reusable (workflow_call) | tidy, lint, test with race detector |
| `release-on-tag.yaml` | version tags (`v*.*.*`) | goreleaser build, container image push, Cloud Run deploy |
| `deploy-saas.yaml` | manual (workflow_dispatch) | Deploy devpulse to Cloud Run |
| `codeql-analysis.yaml` | schedule, push | CodeQL security analysis (Go + JavaScript) |
| `scan-on-schedule.yaml` | schedule | Vulnerability scanning |
| `score-on-schedule.yaml` | schedule | Scheduled reputation scoring |
| `reputation-on-pr.yaml` | PR events | Reputation scoring on PR contributors |

## Supply Chain Security

- **Container images** — built via ko, pushed to GHCR (`ghcr.io/thingzio/devpulse`)
- **Vulnerability scanning** — govulncheck in CI, Trivy on schedule
- **Dependency pinning** — all GitHub Actions pinned by commit hash
- **CODEOWNERS** — `.github/` directory protected

Tool versions and quality thresholds are centralized in `.settings.yaml`.
