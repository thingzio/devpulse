# Architecture

Multi-tenant SaaS for GitHub project health analytics. Three binaries: `devpulse-site` (HTTP server), `devpulse-import` (batch worker), and `devpulse-admin` (IAM-protected admin service). PostgreSQL with Row-Level Security for tenant isolation.

## Data Flow

```
GitHub App webhook ──→ devpulse (serve) ──→ tenant_repo ──→ PostgreSQL (RLS-scoped)
Cloud Scheduler */2h ──→ devpulse (import) ──→ shared token pool ──→ PostgreSQL
Browser ──→ POST /api/repos ──→ public check + install check ──→ tenant_repo ──→ on-demand import
Browser ──→ devpulse (serve) ──→ OAuth ──→ RLS-scoped dashboard
Admin  ──→ devpulse (admin) ──→ IAM auth ──→ tenant plan management ──→ PostgreSQL
```

### Repo-Add Gating

Adding a repo requires two conditions:
1. **Public repo** — verified via HEAD request to GitHub API
2. **Active GitHub App installation** — tenant must have at least one installation (org-specific preferred, falls back to any). This ensures the import job can mint installation tokens for authenticated API access (5,000 req/hr per installation).

The GitHub App installation is separate from OAuth login. Users must install the DevPulseThingz app on their org/account after signing in. The webhook handler records installations in `github_app_installation`.

## Directory Structure

```
devpulse/
├── cmd/devpulse-site/     HTTP server entrypoint (dashboard, OAuth, webhooks, data API)
├── cmd/devpulse-import/   Batch import worker entrypoint
├── cmd/devpulse-admin/    IAM-protected admin service (tenant management)
├── pkg/
│   ├── admin/              IAM-protected admin service handlers
│   ├── server/             HTTP server, handlers, templates, static assets
│   │   ├── static/         Frontend: CSS, JS, images (embedded via go:embed)
│   │   └── templates/      HTML templates: layout, header, footer, home, landing, dashboard, help, tos
│   ├── importer/           Import worker (sharded task-index + goroutine workers)
│   ├── config/             Environment configuration
│   ├── data/               Store interface, shared types, helpers
│   │   ├── postgres/       PostgreSQL Store implementation + migrations
│   │   └── ghutil/         Shared GitHub API helpers (rate limiting, user mapping)
│   ├── tenant/             Tenant CRUD, sessions, GitHub App JWT, installations
│   ├── health/             Health check endpoint
│   ├── middleware/         Auth middleware, tenant scope (RLS via dedicated conn)
│   ├── oauth/              GitHub OAuth web flow
│   ├── plan/               Plan limits (Free, Starter, Pro, Enterprise) and feature gating
│   ├── logging/            JSON structured logging setup
│   └── net/                HTTP client utilities with rate limit handling
├── infra/saas/             Terraform for GCP infrastructure
├── tools/                  Dev scripts (version bump, shared helpers)
├── docs/                   Documentation
├── .github/                CI/CD workflows, composite actions, CODEOWNERS
└── .settings.yaml          Centralized tool versions and quality thresholds
```

## Binaries

Three separate binaries with independent lifecycles. `devpulse-site` serves HTTP, `devpulse-import` runs the full import pipeline (events, reputation, insights), `devpulse-admin` provides IAM-protected tenant management. Each creates its own store via `postgres.NewFromEnv()`.

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
- `sql/migrations/001_initial.sql` — base tables (schema_version)
- `sql/migrations_saas/001_initial.sql` — tenant tables + RLS policies (saas_schema_version)

## Tenant Isolation

PostgreSQL Row-Level Security (RLS) policies filter data per tenant:
- Middleware acquires a dedicated `db.Conn()` per request
- Sets `app.tenant_id` via parameterized `set_config($1, false)`
- Global tables (event, developer, etc.) filtered via JOIN to `tenant_repo`
- Tenant-scoped tables filtered by direct `tenant_id` column

## Import Pipeline

The `devpulse-import` binary runs the full pipeline in a single Cloud Run job (every 2 hours, 3 parallel tasks via deterministic sharding). Each task gets a disjoint slice of repos and runs them through all phases using goroutine workers:

1. **Metadata** — repo stars, forks, language, license, community profile
2. **Events** — PRs, reviews, issues, comments, forks (incremental via pagination state)
3. **PR size backfill** — backfills additions/deletions for recent PRs (bounded by `BACKFILL_MAX_DAYS`, default 90)
4. **Releases** — tags, dates, asset downloads
5. **Metric history** — daily star/fork counts
6. **Container versions** — image version tracking
7. **Reputation** — deep reputation scoring with round-robin token pool
8. **Insights** — LLM-generated observations (optional, requires `ANTHROPIC_API_KEY`)

Unchanged repos (no pushes since last import) are skipped automatically to save API quota. On-demand import is triggered via Cloud Run Jobs API when a user adds a new repo (`pkg/server/import_trigger.go`).

**Token pool:** The import job collects installation tokens from all active GitHub App installations across all tenants, deduplicates by installation ID, and rotates via round-robin (`pkg/data/ghutil/tokenpool.go`). Tokens with < 100 remaining quota are skipped. See [INFRASTRUCTURE.md](INFRASTRUCTURE.md) for throughput analysis and scaling guidance.

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

