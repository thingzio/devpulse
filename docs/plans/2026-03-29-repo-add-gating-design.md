# Repo-Add Gating on GitHub App Installation

## Problem

Users can add repos to their dashboard without having the DevPulse GitHub App installed. The import job then fails with "Bad credentials" because no installation token can be minted. Users get no feedback about the root cause.

## Design

Gate `POST /api/repos` on public repo status and GitHub App installation.

### Validation Flow (Implemented)

```
POST /api/repos {org, repo}
  1. Verify repo is publicly accessible (HEAD to GitHub API)
     → 404? Return 403: "repo not found or not public"
  2. Look up active installation for (tenant_id, org)
     → Found? Use it.
     → Not found? Fall back to any active installation for tenant
       (any installation token can access public repos)
     → No installations at all? Return 400 with install link
  3. AddTenantRepos with plan limit enforcement
```

### Key Design Decision

The original design required an org-specific installation and repo-level access check via `CheckRepoAccess`. This was simplified because:
- Any GitHub App installation token can read public repos regardless of org
- DevPulse only supports public repos
- The tenant just needs *at least one* installation so the import job can mint tokens (12,500 req/hr vs 60 unauthenticated)

`CheckRepoAccess` and `LoadGitHubAppConfig` remain in `pkg/tenant/githubapp.go` for future use (e.g., if private repo support is added).

### Changes Made

1. **`pkg/tenant/installation.go`** — added `GetInstallationForOrg(ctx, db, tenantID, org)` query
2. **`pkg/tenant/githubapp.go`** — added `CheckRepoAccess`, `LoadGitHubAppConfig` (moved from importer)
3. **`pkg/server/server.go`** — `addRepoHandler` gates on public + installation check
4. **`pkg/server/static/js/app.js`** — error messages render install URL as clickable link; Add Repos panel hidden when repo selected
5. **`pkg/server/templates/home.html`** — Add Repos panel moved above Repository Overview; status messages beside search box
6. **`pkg/server/templates/help.html`** — install guidance in Getting Started and Troubleshooting
7. **`docs/BOOTSTRAP.md`** — GitHub App permissions (Packages Read), install step in Verify section
8. **`pkg/data/postgres/container.go`** — handle HTTP 400 gracefully for packages API
9. **`pkg/importer/importer.go`** — uses shared `tenant.LoadGitHubAppConfig()`

### Search Improvement

`availableReposHandler` now uses GitHub `org:` qualifier when the query contains a `/` (e.g., `NVIDIA/NVS` → `NVS in:name org:NVIDIA`), improving prefix matching for org-scoped searches.

### Error Messages

| Condition | HTTP Status | Message |
|-----------|-------------|---------|
| Repo not public | 403 | "repo not found or not public" |
| No installation at all | 400 | "No GitHub App installation found. Go to ... and click Configure to install the app." |
| Plan limit exceeded | 400 | ErrRepoLimitExceeded |
