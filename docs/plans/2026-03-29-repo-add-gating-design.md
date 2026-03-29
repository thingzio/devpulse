# Repo-Add Gating on GitHub App Installation

## Problem

Users can add repos to their dashboard without having the DevPulse GitHub App installed on the target org. The import job then fails with "Bad credentials" because no installation token can be minted. Users get no feedback about the root cause.

## Design

Gate `POST /api/repos` on GitHub App installation status and repo-level access.

### Validation Flow

```
POST /api/repos {org, repo}
  1. Look up active installation for (tenant_id, org)
     → No installation? Return 400: "Install DevPulse app on {org}"
  2. Mint installation token for that installation
  3. Call GET /repos/{org}/{repo} with installation token
     → 404 or 403? Return 400: "DevPulse app doesn't have access to {org}/{repo}"
  4. Existing flow: isPublicRepo check, AddTenantRepos with plan limit
```

### Changes

1. **`pkg/tenant/installation.go`** — add `GetInstallationForOrg(ctx, db, tenantID, org)` query
2. **`pkg/tenant/github.go`** (or similar) — add `CheckRepoAccess(ctx, cfg, installationID, org, repo)` that mints token and checks repo accessibility via GitHub API
3. **`pkg/server/server.go`** — update `addRepoHandler` to call both checks before the existing flow
4. **`pkg/server/templates/help.html`** — already updated with install guidance (done)

### What Stays the Same

- Search endpoint (`GET /api/repos/available`) unchanged
- Webhook handlers unchanged
- Import flow unchanged
- Frontend JS unchanged (error message displayed via existing `$status.text(xhr.responseText)`)

### Error Messages

| Condition | HTTP Status | Message |
|-----------|-------------|---------|
| No installation for org | 400 | "DevPulse app is not installed on {org}. Install it at https://github.com/apps/DevPulseThingz" |
| Installation exists, repo not accessible | 400 | "DevPulse app does not have access to {org}/{repo}. Update repository permissions in your GitHub organization settings." |
| Repo not public | 403 | Existing: "repo not found or not public" |
| Plan limit exceeded | 400 | Existing: ErrRepoLimitExceeded |
