# DevPulse Cloud: Multi-Tenant SaaS Design

**Date**: 2026-03-26
**Status**: Approved
**Domain**: devpulse.thingz.io
**Repo**: github.com/thingzio/devpulse

## Overview

Transform DevPulse from a single-tenant CLI/hosted tool into a multi-tenant SaaS offering. Users sign in with GitHub, install a GitHub App on their repos, and get a hosted analytics dashboard. The existing self-hosted CLI remains unchanged.

## Decisions

| Decision | Choice | Rationale |
|---|---|---|
| Target users | Individuals/small teams, designed for enterprise growth | Start simple, layer complexity |
| Identity | GitHub OAuth (web flow) | Every user already has GitHub; no separate identity system |
| Data access | GitHub App (installation tokens) | Per-org rate limits, auto-refresh, fine-grained scoping |
| Data isolation | Row-level `tenant_id` + PostgreSQL RLS | Lowest cost, single schema, DB-enforced isolation |
| Shared data | Events/reputation/developers are global; access scoped via `tenant_repo` join | Avoids duplicate imports for shared public repos |
| Import model | Scheduled worker loop (designed for Cloud Tasks migration) | Simple now, queue-ready later |
| Repo config | API/UI only, `tenant_repo` table | SaaS users expect UI, not config files |
| Metering | Per-repo limits (`tenant.max_repos`) | Simple to enforce, maps to value delivered |
| Multi-user | `tenant_member` table, owner invites GitHub users | One tenant = one account; multiple users can view |
| Codebase | Monorepo, two binaries (`devpulse` + `devpulse-cloud`) | Shared query/dashboard code, independent entrypoints |
| Deploy | Single region (us-west1), new GCP project, CDN as fast follow | Lowest cost, acceptable latency for dashboards |
| Infra | Terraform for all GCP resources | Reproducibility, state in GCS bucket |
| ToS | Required day 1, accept-before-use gate | Must not take data without ToS in place |
| Billing | Stripe (deferred); manual plan upgrades for now | Free tier default, admin-upgradable |
| Landing page | Separate effort; simple initially, AICR-style aspiration | Not blocking SaaS launch |
| Notifications | Dashboard only for v1 | No email infrastructure needed at launch |
| Migration | No data porting; existing hosted offering sunsets | Clean start, self-hosted CLI preserved |

## Architecture

```
                        Internet
                           |
              +------------+------------+
              |                         |
        Cloud Run                  GitHub
        devpulse-cloud             Webhooks
        (serve mode)               (app install/remove)
              |                         |
              |                    Cloud Run
              |                    devpulse-cloud
              |                    (webhook endpoint)
              |                         |
        +-----+-------------------------+-----+
        |           Cloud SQL Postgres         |
        |  tenant, tenant_member,              |
        |  tenant_repo,                        |
        |  github_app_installation,            |
        |  session,                            |
        |  + existing tables (global data,     |
        |    RLS-scoped via tenant_repo)       |
        +-----+-------------------------------+
              |
        Cloud Scheduler (hourly)
              |
        Cloud Run Job
        devpulse-cloud --import
        (iterates tenants, mints tokens,
         imports repos)
```

Single binary (`devpulse-cloud`) with three modes:
- **`serve`** — HTTP server: dashboard, API, OAuth callbacks, webhook endpoint
- **`import`** — Scheduled worker: tenant iteration, data import

Webhook handling is an endpoint on the serve mode (no separate service).

## Package Structure

### New packages

```
cmd/devpulse-cloud/         # SaaS entrypoint (serve, import subcommands)
pkg/tenant/                  # Tenant CRUD, GitHub App logic, installation token minting
pkg/tenant/scoped.go        # ScopedStore: RLS-based tenant isolation wrapper
pkg/middleware/              # Auth, tenant context injection, CSRF
pkg/oauth/                   # GitHub OAuth web flow (sign-in/callback)
```

### Unchanged packages

```
cmd/devpulse/               # Self-hosted CLI (untouched)
pkg/cli/                    # CLI commands, handlers, templates, static assets
pkg/data/                   # Store interface, types, helpers (tenant-unaware)
pkg/data/sqlite/            # SQLite implementation (self-hosted only)
pkg/data/postgres/          # PostgreSQL implementation (shared queries)
pkg/data/ghutil/            # GitHub API helpers
pkg/auth/                   # Device code OAuth (CLI only)
```

### Tenant scoping via PostgreSQL RLS

Existing Store methods remain unchanged. Tenant isolation is enforced at the database level:

1. Middleware sets PostgreSQL session variable: `SET app.tenant_id = '<uuid>'`
2. RLS policies filter all queries automatically
3. Insert triggers auto-set `tenant_id` on new rows
4. Global tables (event, developer, reputation) use RLS policies that join through `tenant_repo`

```sql
-- RLS policy for global tables: tenant sees data only for their tracked repos
CREATE POLICY tenant_repo_filter ON event
    USING (
        EXISTS (
            SELECT 1 FROM tenant_repo tr
            WHERE tr.tenant_id = current_setting('app.tenant_id')::uuid
              AND tr.org = event.org
              AND tr.repo = event.repo
              AND tr.active = true
        )
    );

-- Trigger for tenant-scoped tables: auto-set tenant_id on insert
CREATE OR REPLACE FUNCTION set_tenant_id() RETURNS TRIGGER AS $$
BEGIN
    NEW.tenant_id = current_setting('app.tenant_id')::uuid;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
```

Import worker uses an admin connection (no RLS) when iterating tenants, then sets tenant scope per-tenant for scoped inserts.

## Data Model

### New tables

```sql
CREATE TABLE tenant (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    github_id   BIGINT UNIQUE NOT NULL,
    username    TEXT NOT NULL,
    email       TEXT,
    avatar_url  TEXT,
    max_repos   INT NOT NULL DEFAULT 3,
    plan        TEXT NOT NULL DEFAULT 'free',
    tos_accepted_at TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE tenant_member (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   UUID NOT NULL REFERENCES tenant(id) ON DELETE CASCADE,
    github_id   BIGINT NOT NULL,
    username    TEXT NOT NULL,
    role        TEXT NOT NULL DEFAULT 'viewer',
    invited_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    accepted_at TIMESTAMPTZ,
    UNIQUE(tenant_id, github_id)
);

CREATE TABLE github_app_installation (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id           UUID NOT NULL REFERENCES tenant(id) ON DELETE CASCADE,
    installation_id     BIGINT UNIQUE NOT NULL,
    target_type         TEXT NOT NULL,
    target_login        TEXT NOT NULL,
    permissions         JSONB,
    suspended_at        TIMESTAMPTZ,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE tenant_repo (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   UUID NOT NULL REFERENCES tenant(id) ON DELETE CASCADE,
    org         TEXT NOT NULL,
    repo        TEXT NOT NULL,
    reputation  JSONB,
    insight     JSONB,
    active      BOOLEAN NOT NULL DEFAULT TRUE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(tenant_id, org, repo)
);

CREATE TABLE session (
    id          TEXT PRIMARY KEY,
    tenant_id   UUID NOT NULL REFERENCES tenant(id) ON DELETE CASCADE,
    expires_at  TIMESTAMPTZ NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

### Shared vs. tenant-scoped data

| Layer | Tables | Isolation |
|---|---|---|
| **Global** | event, developer, repo_meta, release, container_version, reputation, community_health | No `tenant_id`; RLS joins through `tenant_repo` |
| **Tenant-scoped** | tenant, tenant_member, tenant_repo, session, github_app_installation, repo_insights, import_state | `tenant_id` column, direct RLS |

## Security Model

### Token lifecycle

| Secret | Storage | Lifetime |
|---|---|---|
| GitHub App private key | GCP Secret Manager | Long-lived, rotatable |
| GitHub App installation ID | Database (not a secret) | Until uninstall |
| GitHub installation token | In-memory only | 1 hour, never persisted |
| GitHub OAuth token | Never stored | Used once at sign-in, discarded |
| Session token | Database (SHA-256 hashed) + browser cookie | 7 days, revocable |

### Key principles

- **No long-lived credentials in the database.** Installation tokens are minted on demand from the App's private key (in Secret Manager) and the installation ID.
- **OAuth token discarded immediately after identity verification.** GitHub App handles all API access.
- **Server-side sessions** with `__Host-` prefixed cookies: `Secure; HttpOnly; SameSite=Strict; Path=/`.
- **CSRF protection** via `SameSite=Strict` cookie + `Origin` header verification on mutations.
- **Cloud SQL**: private IP only, IAM auth, encrypted at rest, SSL enforced.
- **Audit logging**: sign-ins, installations, repo additions logged via structured slog.

## Authentication Flow

### Sign-in

1. User clicks "Sign in with GitHub"
2. Redirect to `github.com/login/oauth/authorize` with `scope=read:user,user:email`
3. GitHub redirects to `/auth/github/callback` with authorization code
4. Server exchanges code for OAuth token, calls `GET /user` to get identity
5. OAuth token discarded
6. Upsert `tenant` row by `github_id`
7. Check `tos_accepted_at` — if null, redirect to ToS acceptance page
8. Look up `tenant_member` rows — if multiple tenants, show picker
9. Create session: random 256-bit token, store SHA-256 hash in `session` table
10. Set `__Host-session` cookie, redirect to dashboard

### GitHub App installation

1. User clicks "Add repos" in dashboard
2. Redirect to `github.com/apps/devpulse/installations/new`
3. User selects orgs/repos, clicks Install
4. GitHub sends webhook `POST /webhook/github` (event: `installation`)
5. Server verifies HMAC-SHA256 signature, stores installation, auto-populates `tenant_repo`

### Webhook events handled

| Event | Action |
|---|---|
| `installation` created | Store installation, add repos |
| `installation` deleted | Deactivate installation and repos |
| `installation_repositories` added | Add `tenant_repo` rows |
| `installation_repositories` removed | Deactivate removed repos |
| `installation` suspended | Set `suspended_at`, pause imports |

## Import Worker

### Current design (scheduled loop)

```
Cloud Scheduler (hourly)
  → Cloud Run Job: devpulse-cloud --import
    → Query active tenants with active repos
    → For each tenant:
      → Get installations (skip suspended)
      → Mint installation token (JWT → GitHub API)
      → Set PostgreSQL session var (tenant scope)
      → For each repo:
        → Check staleness (import_state)
        → If stale: run existing Import* methods
        → Log sync_summary with tenant context
      → Continue on error (don't fail entire run)
```

### Queue migration path (future)

The `importTenant` function becomes the unit of work for Cloud Tasks:

- `runImport` enqueues one message per tenant instead of looping
- Cloud Run processes messages using the same `importTenant` function
- Only the dispatch layer changes, not the import logic

## Routing

### Public routes (no auth)

```
GET  /                          Landing page
GET  /auth/github               Start OAuth flow
GET  /auth/github/callback      OAuth callback
POST /webhook/github            GitHub App webhooks
GET  /tos                       Terms of Service page
POST /tos/accept                Accept ToS
GET  /static/assets/*           Static files (shared embed.FS)
```

### Authenticated routes

```
GET  /dashboard                 Main dashboard (existing template + auth wrapper)
POST /auth/signout              Sign out

GET  /api/repos                 List tenant's tracked repos
POST /api/repos                 Add repo to tracking
DELETE /api/repos/{id}          Remove repo
GET  /api/installations         List GitHub App installations
GET  /api/members               List tenant members
POST /api/members               Invite member
DELETE /api/members/{id}        Remove member

GET  /data/*                    All existing data/insights endpoints
                                (reused handlers, RLS handles scoping)
```

## Infrastructure (Terraform)

### Layout

```
infra/saas/
  main.tf              Provider, backend (GCS bucket)
  variables.tf         Project ID, region, domain
  project.tf           GCP project, enabled APIs
  network.tf           VPC, private service access
  database.tf          Cloud SQL Postgres (db-f1-micro)
  secrets.tf           Secret Manager (GitHub App key)
  cloudrun.tf          Cloud Run service + job
  scheduler.tf         Cloud Scheduler (hourly import trigger)
  iam.tf               Service accounts, least-privilege roles
  dns.tf               Cloud DNS zone for devpulse.thingz.io
  monitoring.tf        Uptime checks, log-based metrics, alerts
  outputs.tf           Service URL, DB connection name
```

### State

```hcl
terraform {
  backend "gcs" {
    bucket = "devpulse-saas-tf-state"
    prefix = "infra/"
  }
}
```

### Resources

| Resource | Config |
|---|---|
| GCP Project | New project, billing linked |
| VPC | Private service access for Cloud SQL |
| Cloud SQL Postgres | db-f1-micro, private IP, SSL, automated backups, IAM auth |
| Secret Manager | GitHub App private key |
| Cloud Run service | devpulse-cloud serve, min instances=0 (scale to zero) |
| Cloud Run job | devpulse-cloud import, triggered hourly |
| Cloud Scheduler | Hourly cron for import job |
| Service accounts | Separate SAs for service vs. job, least-privilege |
| Cloud DNS | Zone for devpulse.thingz.io |
| Monitoring | Uptime checks, error rate alerts, import failure alerts |

### Not managed by Terraform

- Schema migrations (binary handles at startup)
- GitHub App registration (manual, one-time)
- Docker images (built by GitHub Actions CI/CD)
- DNS NS delegation at registrar (manual)

## Cost Estimates

| Tenants | Repos | Cloud SQL | Cloud Run | Total/mo |
|---|---|---|---|---|
| 10 | 30 | ~$7 | ~$2 | ~$9 |
| 100 | 300 | ~$25 | ~$10 | ~$35 |
| 1,000 | 3,000 | ~$50 | ~$40 | ~$90 |
| 10,000 | 30,000 | ~$150 | ~$100 | ~$250 |

### Scaling levers (in order of need)

1. Import frequency tuning (staleness config per repo)
2. PgBouncer sidecar for connection pooling (~100+ Cloud Run instances)
3. Read replica for dashboard queries (~1,000+ tenants)
4. CDN for static assets + API caching (fast follow)
5. Cloud Tasks for parallel per-tenant imports (when job duration exceeds 1hr)

### Not needed at launch

- Redis/Memcached caching layer
- Read replicas
- CDN (fast follow, not blocker)
- Materialized views

## Open Items (Deferred)

- Landing page design and content (separate effort)
- Stripe billing integration (when paid tiers needed)
- CDN layer (fast follow after launch)
- Cloud Tasks migration (when import job duration warrants it)
- Enterprise SSO/SAML (when enterprise tier needed)
- Email notifications (dashboard-only for v1)
