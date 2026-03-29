# Repo-Add Gating Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Prevent users from adding repos unless the DevPulse GitHub App is installed on the org and has access to the specific repo.

**Architecture:** Add two checks to `addRepoHandler` before the existing flow: (1) DB lookup for active installation on the org, (2) GitHub API call with installation token to verify repo access. Both use existing `tenant` package patterns.

**Tech Stack:** Go, PostgreSQL, GitHub REST API, `github.com/golang-jwt/jwt/v5`

---

### Task 1: Add `GetInstallationForOrg` query

**Files:**
- Modify: `pkg/tenant/installation.go`
- Modify: `pkg/tenant/import.go`

**Step 1: Add SQL constant and function to `installation.go`**

Add after line 73 (after `getTenantMaxReposSQL`):

```go
const getInstallationForOrgSQL = `
	SELECT installation_id, target_login
	FROM github_app_installation
	WHERE tenant_id = $1 AND target_login = $2 AND suspended_at IS NULL
	LIMIT 1`
```

Add after `DeactivateTenantRepo` (after line 173):

```go
// GetInstallationForOrg returns the active installation for a tenant's org, if any.
func GetInstallationForOrg(ctx context.Context, db *sql.DB, tenantID, org string) (*ActiveInstallation, error) {
	var inst ActiveInstallation
	err := db.QueryRowContext(ctx, getInstallationForOrgSQL, tenantID, org).Scan(&inst.ID, &inst.Login)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("querying installation for org %s: %w", org, err)
	}
	return &inst, nil
}
```

Note: `ActiveInstallation` type is already defined in `import.go:39-42`. Returns `nil, nil` when no installation exists (not an error).

**Step 2: Run tests**

Run: `make test`
Expected: PASS (no behavior change yet)

**Step 3: Commit**

```bash
git add pkg/tenant/installation.go
git commit -S -m "feat: add GetInstallationForOrg query"
```

---

### Task 2: Add `CheckRepoAccess` to `githubapp.go`

**Files:**
- Modify: `pkg/tenant/githubapp.go`

**Step 1: Add `CheckRepoAccess` function**

Add after `MintInstallationToken` (after line 82):

```go
// CheckRepoAccess verifies that a GitHub App installation can access a specific repo.
// It mints an installation token and calls GET /repos/{org}/{repo}.
// Returns true if accessible, false if not (404/403), or error on unexpected failure.
func CheckRepoAccess(ctx context.Context, cfg *GitHubAppConfig, installationID int64, org, repo string) (bool, error) {
	token, err := MintInstallationToken(ctx, cfg, installationID)
	if err != nil {
		return false, fmt.Errorf("minting token for access check: %w", err)
	}

	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = "https://api.github.com"
	}

	repoURL := fmt.Sprintf("%s/repos/%s/%s", baseURL, org, repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, repoURL, nil)
	if err != nil {
		return false, fmt.Errorf("creating repo access request: %w", err)
	}
	req.Header.Set("Authorization", "token "+token.Token)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return false, fmt.Errorf("checking repo access: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		return true, nil
	}
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusForbidden {
		return false, nil
	}

	return false, fmt.Errorf("unexpected status checking repo access: %d", resp.StatusCode)
}
```

**Step 2: Run tests**

Run: `make test`
Expected: PASS

**Step 3: Commit**

```bash
git add pkg/tenant/githubapp.go
git commit -S -m "feat: add CheckRepoAccess for installation-scoped repo validation"
```

---

### Task 3: Move `loadGitHubAppConfig` to shared location

**Files:**
- Modify: `pkg/tenant/githubapp.go`
- Modify: `pkg/importer/importer.go`

The `loadGitHubAppConfig` function in `pkg/importer/importer.go:280-311` is needed by the server too. Move it to `pkg/tenant/githubapp.go` as `LoadGitHubAppConfig` (exported).

**Step 1: Add to `pkg/tenant/githubapp.go`**

Add imports `crypto/x509`, `encoding/pem`, `os`, `strconv` and the function after `CheckRepoAccess`:

```go
// LoadGitHubAppConfig reads GitHub App credentials from environment variables.
func LoadGitHubAppConfig() (*GitHubAppConfig, error) {
	appIDStr := os.Getenv("GITHUB_APP_ID")
	keyPath := os.Getenv("GITHUB_APP_KEY_PATH")

	if appIDStr == "" || keyPath == "" {
		return nil, fmt.Errorf("GITHUB_APP_ID and GITHUB_APP_KEY_PATH are required")
	}

	appID, err := strconv.ParseInt(appIDStr, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("parsing GITHUB_APP_ID: %w", err)
	}

	keyData, err := os.ReadFile(keyPath) //nolint:gosec // path from trusted GITHUB_APP_KEY_PATH env var
	if err != nil {
		return nil, fmt.Errorf("reading private key from %s: %w", keyPath, err)
	}

	block, _ := pem.Decode(keyData)
	if block == nil {
		return nil, fmt.Errorf("no PEM block found in %s", keyPath)
	}

	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parsing private key: %w", err)
	}

	return &GitHubAppConfig{
		AppID:      appID,
		PrivateKey: key,
	}, nil
}
```

**Step 2: Update importer to use shared function**

In `pkg/importer/importer.go`, replace the `loadGitHubAppConfig()` call at line 248 with `tenant.LoadGitHubAppConfig()`, and delete the local `loadGitHubAppConfig` function (lines 279-311). Remove unused imports (`crypto/x509`, `encoding/pem`) from importer if they become unused.

**Step 3: Run tests**

Run: `make qualify`
Expected: PASS

**Step 4: Commit**

```bash
git add pkg/tenant/githubapp.go pkg/importer/importer.go
git commit -S -m "refactor: move LoadGitHubAppConfig to tenant package for reuse"
```

---

### Task 4: Update `addRepoHandler` with installation gating

**Files:**
- Modify: `pkg/server/server.go:400-432`

**Step 1: Update handler**

Replace the `addRepoHandler` function body (lines 400-432) with:

```go
func addRepoHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tn := middleware.TenantFromContext(r.Context())
		if tn == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		org := r.FormValue("org")
		repo := r.FormValue("repo")
		if org == "" || repo == "" {
			http.Error(w, "org and repo required", http.StatusBadRequest)
			return
		}

		// 1. Check for active GitHub App installation on the org.
		install, err := tenant.GetInstallationForOrg(r.Context(), db, tn.ID, org)
		if err != nil {
			slog.Error("checking installation", "org", org, "error", err)
			http.Error(w, "error checking installation", http.StatusInternalServerError)
			return
		}
		if install == nil {
			http.Error(w, fmt.Sprintf(
				"DevPulse app is not installed on %s. Install it at https://github.com/apps/DevPulseThingz", org),
				http.StatusBadRequest)
			return
		}

		// 2. Verify the installation has access to this specific repo.
		cfg, err := tenant.LoadGitHubAppConfig()
		if err != nil {
			slog.Error("loading github app config", "error", err)
			http.Error(w, "error checking repo access", http.StatusInternalServerError)
			return
		}

		accessible, err := tenant.CheckRepoAccess(r.Context(), cfg, install.ID, org, repo)
		if err != nil {
			slog.Error("checking repo access", "org", org, "repo", repo, "error", err)
			http.Error(w, "error checking repo access", http.StatusInternalServerError)
			return
		}
		if !accessible {
			http.Error(w, fmt.Sprintf(
				"DevPulse app does not have access to %s/%s. Update repository permissions in your GitHub organization settings.", org, repo),
				http.StatusBadRequest)
			return
		}

		// 3. Verify the repo is publicly accessible.
		if !isPublicRepo(r.Context(), org, repo) {
			http.Error(w, "repo not found or not public", http.StatusForbidden)
			return
		}

		if err := tenant.AddTenantRepos(r.Context(), db, tn.ID, []tenant.OrgRepo{{Org: org, Repo: repo}}); err != nil {
			slog.Error("adding repo", "error", err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		w.WriteHeader(http.StatusCreated)
	}
}
```

Ensure `fmt` and `log/slog` are in the imports for `server.go` (they already are).

**Step 2: Run tests**

Run: `make qualify`
Expected: PASS

**Step 3: Commit**

```bash
git add pkg/server/server.go
git commit -S -m "feat: gate repo addition on GitHub App installation and repo access"
```

---

### Task 5: Verify end-to-end locally

**Step 1: Run dev server**

Run: `make server`

**Step 2: Manual test scenarios**

1. Sign in, try adding a repo from an org without the app installed → expect "DevPulse app is not installed on {org}" error in the UI
2. Install app on an org with selected repos, try adding an excluded repo → expect "does not have access" error
3. Add a repo the app does have access to → expect success

**Step 3: Run full qualification**

Run: `make qualify`
Expected: PASS (all tests, lint, vulncheck)
