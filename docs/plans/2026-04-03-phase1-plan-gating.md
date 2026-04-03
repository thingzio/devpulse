# Phase 1: Plan Feature Gating Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Extend the plan system with Starter tier and gate features (repos, data retention, PDF/CSV export, AI insights, deep reputation) by plan level. Add admin invite endpoint for pre-creating tenants.

**Architecture:** Extend `pkg/plan.Limits` struct with new fields. Gate at three layers: importer (AI, reputation), data API handlers (date clamp, export, AI display), and templates (PDF button visibility). Plan limits looked up at runtime via `plan.Get(tenant.Plan)` — no new DB columns. Admin invite creates tenant row then sets plan via existing `UpdatePlan`.

**Tech Stack:** Go, PostgreSQL, `encoding/csv` + `archive/zip` (stdlib), jsPDF (existing), testify

---

## Task 1: Extend Plan Constants and Limits Struct

**Files:**
- Modify: `pkg/plan/plan.go`
- Modify: `pkg/plan/plan_test.go`

**Step 1: Update the Limits struct and plan map**

In `pkg/plan/plan.go`, add `Starter` constant, new fields to `Limits`, update `All` map:

```go
package plan

const (
	Free       = "free"
	Starter    = "starter"
	Pro        = "pro"
	Enterprise = "enterprise"
)

type Limits struct {
	MaxRepos           int  // 0 = unlimited
	MaxEventsPerWeek   int  // 0 = unlimited
	MaxDataRangeMonths int  // 0 = unlimited
	AILevel            int  // 0=none, 1=insights, 2=insights+actions
	DeepReputation     bool
	PDFExport          bool
	CSVExport          bool
}

var All = map[string]Limits{
	Free:       {MaxRepos: 1, MaxEventsPerWeek: 500, MaxDataRangeMonths: 3, AILevel: 0, DeepReputation: false, PDFExport: false, CSVExport: false},
	Starter:    {MaxRepos: 5, MaxEventsPerWeek: 2500, MaxDataRangeMonths: 12, AILevel: 1, DeepReputation: false, PDFExport: true, CSVExport: false},
	Pro:        {MaxRepos: 25, MaxEventsPerWeek: 15000, MaxDataRangeMonths: 36, AILevel: 2, DeepReputation: true, PDFExport: true, CSVExport: true},
	Enterprise: {MaxRepos: 0, MaxEventsPerWeek: 0, MaxDataRangeMonths: 0, AILevel: 2, DeepReputation: true, PDFExport: true, CSVExport: true},
}
```

`Get` and `FreeLimits` functions remain unchanged.

**Step 2: Update tests**

In `pkg/plan/plan_test.go`, update `TestGet` table to include Starter and new field assertions, update `TestFreeLimits` for new Free limits:

```go
func TestGet(t *testing.T) {
	tests := []struct {
		plan               string
		wantOK             bool
		wantMaxRepos       int
		wantMaxEvents      int
		wantMaxDataMonths  int
		wantAILevel        int
		wantDeepReputation bool
		wantPDFExport      bool
		wantCSVExport      bool
	}{
		{Free, true, 1, 500, 3, 0, false, false, false},
		{Starter, true, 5, 2500, 12, 1, false, true, false},
		{Pro, true, 25, 15000, 36, 2, true, true, true},
		{Enterprise, true, 0, 0, 0, 2, true, true, true},
		{"unknown", false, 0, 0, 0, 0, false, false, false},
		{"", false, 0, 0, 0, 0, false, false, false},
		{"FREE", false, 0, 0, 0, 0, false, false, false},
	}

	for _, tc := range tests {
		t.Run(tc.plan, func(t *testing.T) {
			limits, ok := Get(tc.plan)
			assert.Equal(t, tc.wantOK, ok)
			if tc.wantOK {
				require.True(t, ok)
				assert.Equal(t, tc.wantMaxRepos, limits.MaxRepos)
				assert.Equal(t, tc.wantMaxEvents, limits.MaxEventsPerWeek)
				assert.Equal(t, tc.wantMaxDataMonths, limits.MaxDataRangeMonths)
				assert.Equal(t, tc.wantAILevel, limits.AILevel)
				assert.Equal(t, tc.wantDeepReputation, limits.DeepReputation)
				assert.Equal(t, tc.wantPDFExport, limits.PDFExport)
				assert.Equal(t, tc.wantCSVExport, limits.CSVExport)
			}
		})
	}
}

func TestFreeLimits(t *testing.T) {
	l := FreeLimits()
	assert.Equal(t, 1, l.MaxRepos)
	assert.Equal(t, 500, l.MaxEventsPerWeek)
	assert.Equal(t, 3, l.MaxDataRangeMonths)
	assert.Equal(t, 0, l.AILevel)
	assert.False(t, l.DeepReputation)
	assert.False(t, l.PDFExport)
	assert.False(t, l.CSVExport)
}
```

**Step 3: Run tests**

Run: `make test`
Expected: All tests pass. Existing code that only reads `MaxRepos`/`MaxEventsPerWeek` is unaffected since new fields are additive.

**Step 4: Update admin upgrade error message**

In `pkg/admin/admin.go:238`, update the plan validation error message to include `starter`:

```go
"invalid plan: %s (must be free, starter, pro, or enterprise)", req.Plan,
```

**Step 5: Update tenant-upgrade tool**

In `tools/tenant-upgrade`, update usage text and case statement to include `starter`:

```bash
usage() {
    echo "Usage: $0 <username> <plan>"
    echo ""
    echo "Plans:"
    echo "  free        1 repo, 500 events/week"
    echo "  starter     5 repos, 2,500 events/week"
    echo "  pro         25 repos, 15,000 events/week"
    echo "  enterprise  unlimited"
    exit 1
}
```

And the case:
```bash
case "$PLAN" in
    free|starter|pro|enterprise) ;;
    *) err "Unknown plan: $PLAN (must be free, starter, pro, or enterprise)" ;;
esac
```

**Step 6: Run qualify and commit**

Run: `make qualify`
Expected: PASS

```bash
git add pkg/plan/plan.go pkg/plan/plan_test.go pkg/admin/admin.go tools/tenant-upgrade
git commit -S -m "feat: add Starter tier and extend Limits struct with gating fields"
```

---

## Task 2: Gate Deep Reputation and AI in Importer

**Files:**
- Modify: `pkg/importer/importer.go`

**Context:** Currently `importRepo` receives `(ctx, store, token, org, repo, llmCfg)` — no tenant/plan info. `importClaim` has the tenant but doesn't pass plan data through. We need to thread the plan string so `importRepo` can look up limits.

**Step 1: Add plan parameter to importRepo**

Change `importRepo` signature to accept `planName string`:

```go
func importRepo(ctx context.Context, store data.Store, token, org, repo string, llmCfg *data.LLMConfig, planName string) error {
```

Update the call site in `importClaim` (line 159):

```go
return importRepo(ctx, store, token, claim.Org, claim.Repo, llmCfg, tn.Plan)
```

**Step 2: Gate deep reputation by plan**

Replace the token-only check at line 204 with a plan-aware check. Look up limits via `plan.Get(planName)`:

```go
limits, _ := plan.Get(planName)
if token != "" && limits.DeepReputation {
    slog.Info("phase: deep reputation", "org", org, "repo", repo)
    tokenFn := func() string { return token }
    if res, err := store.ImportDeepReputation(ctx, tokenFn, deepReputationDefaultLimit, 0, &org, &repo); err != nil {
        slog.Error("importing deep reputation", "org", org, "repo", repo, "error", err)
        errs++
    } else {
        slog.Info("deep reputation complete", "org", org, "repo", repo, "scored", res.Scored, "errors", res.Errors)
    }
} else if token != "" {
    slog.Debug("skipping deep reputation, not included in plan", "org", org, "repo", repo, "plan", planName)
}
```

**Step 3: Gate AI insights by plan**

Replace the `llmCfg != nil` check at line 216 with a plan-aware check:

```go
if llmCfg != nil && limits.AILevel > 0 {
    slog.Info("phase: insights", "org", org, "repo", repo)
    if err := generateRepoInsights(ctx, store, llmCfg, org, repo); err != nil {
        slog.Error("generating insights", "org", org, "repo", repo, "error", err)
        errs++
    }
} else if llmCfg != nil {
    slog.Debug("skipping insights, not included in plan", "org", org, "repo", repo, "plan", planName)
}
```

Note: `limits` is already declared above from the deep reputation gate. Move the `plan.Get` call to just before the deep reputation block so both gates can use it.

**Step 4: Add plan import**

Add `"github.com/thingzio/devpulse/pkg/plan"` to the import block in `importer.go`.

**Step 5: Run tests and commit**

Run: `make qualify`
Expected: PASS (existing tests don't cover importRepo directly — they use integration tests with the full DB)

```bash
git add pkg/importer/importer.go
git commit -S -m "feat: gate deep reputation and AI insights by tenant plan"
```

---

## Task 3: Clamp Data Retention in API Handlers

**Files:**
- Modify: `pkg/server/data.go`

**Context:** `parseInsightParams` reads `?m` (months) from query params. All 30+ data handlers call it. We clamp months here using the tenant's plan.

**Step 1: Add plan-based month clamping to parseInsightParams**

`parseInsightParams` doesn't have access to the request's tenant. Change it to accept `*http.Request` (it already does) and look up the tenant from context:

```go
func parseInsightParams(r *http.Request) insightParams {
	months := queryParamInt(r, "m", data.EventAgeMonthsDefault)

	// Clamp months by plan's data retention limit
	if tn := middleware.TenantFromContext(r.Context()); tn != nil {
		if limits, ok := plan.Get(tn.Plan); ok && limits.MaxDataRangeMonths > 0 {
			if months > limits.MaxDataRangeMonths {
				months = limits.MaxDataRangeMonths
			}
		}
	}

	org := r.URL.Query().Get("o")
	repo := r.URL.Query().Get("r")
	if orgStr, repoStr, ok := parseRepo(optional(repo)); ok {
		org = *orgStr
		repo = *repoStr
	}
	return insightParams{months: months, org: optional(org), repo: optional(repo)}
}
```

**Step 2: Add imports**

Add to the import block in `data.go`:
```go
"github.com/thingzio/devpulse/pkg/middleware"
"github.com/thingzio/devpulse/pkg/plan"
```

Check if these are already imported — `middleware` may not be imported yet in `data.go`.

**Step 3: Run tests and commit**

Run: `make qualify`
Expected: PASS — all handlers automatically inherit the clamp.

```bash
git add pkg/server/data.go
git commit -S -m "feat: clamp data API date range by plan retention limit"
```

---

## Task 4: Gate AI Action Items at Display Layer

**Files:**
- Modify: `pkg/server/data.go` (function `insightsGeneratedAPIHandler` at line 546)

**Context:** Full insights (observations + actions) are always generated for paying tiers. For Starter (AILevel=1), strip actions from the API response. For Free (AILevel=0), the importer never generates insights so the response is empty anyway.

**Step 1: Strip actions when AILevel < 2**

Update `insightsGeneratedAPIHandler`:

```go
func insightsGeneratedAPIHandler(store data.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s := storeFromRequest(r, store)
		p := parseInsightParams(r)
		res, err := s.GetRepoInsights(r.Context(), p.org, p.repo)
		if err != nil {
			slog.Error("failed to get generated insights", "error", err)
			writeError(w, http.StatusInternalServerError, "error querying generated insights")
			return
		}

		// Strip action items for plans below Pro
		if res != nil && res.Insights != nil {
			if tn := middleware.TenantFromContext(r.Context()); tn != nil {
				if limits, ok := plan.Get(tn.Plan); ok && limits.AILevel < 2 {
					res.Insights.Actions = nil
				}
			}
		}

		writeJSON(w, http.StatusOK, res)
	}
}
```

**Step 2: Run tests and commit**

Run: `make qualify`
Expected: PASS

```bash
git add pkg/server/data.go
git commit -S -m "feat: strip AI action items from response for non-Pro plans"
```

---

## Task 5: Add Plan Features to Dashboard Template Data

**Files:**
- Modify: `pkg/server/server.go` (function `dashboardHandler` at line 275)

**Context:** The dashboard template needs plan feature flags to conditionally show/hide PDF button and (later) export tab. Pass plan limits alongside existing template data.

**Step 1: Add plan limits to dashboard template data**

Update `dashboardHandler` to include plan features:

```go
func dashboardHandler(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tn := middleware.TenantFromContext(r.Context())
		if tn == nil {
			http.Redirect(w, r, "/auth/github", http.StatusFound)
			return
		}

		limits, _ := plan.Get(tn.Plan)

		t := pageTemplates["home.html"]
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := t.ExecuteTemplate(w, "home", map[string]any{
			"base_path":        "",
			"version":          opts.Version,
			"commit":           opts.Commit,
			"build_date":       opts.Date,
			"period_months":    6,
			"username":         tn.Username,
			"plan":             tn.Plan,
			"pdf_export":       limits.PDFExport,
			"csv_export":       limits.CSVExport,
			"max_data_months":  limits.MaxDataRangeMonths,
			"ai_level":         limits.AILevel,
		}); err != nil {
			slog.Error("rendering dashboard", "error", err)
		}
	}
}
```

**Step 2: Run tests and commit**

Run: `make qualify`
Expected: PASS — template ignores unknown keys.

```bash
git add pkg/server/server.go
git commit -S -m "feat: pass plan feature flags to dashboard template"
```

---

## Task 6: Gate PDF Button in Template

**Files:**
- Modify: `pkg/server/templates/home.html`

**Context:** The PDF download button is at line 38. Hide it when `pdf_export` is false. Go templates use `{{if .pdf_export}}`.

**Step 1: Wrap PDF button in conditional**

Replace the PDF button block (lines 38-45) with:

```html
{{if .pdf_export}}
<button class="pdf-btn" id="pdf-download" disabled aria-label="Download PDF report" title="Download PDF report">
    <svg class="pdf-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
        <path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"></path>
        <polyline points="14 2 14 8 20 8"></polyline>
        <line x1="16" y1="13" x2="8" y2="13"></line>
        <line x1="16" y1="17" x2="8" y2="17"></line>
    </svg>
    <span class="pdf-label">PDF</span>
</button>
{{end}}
```

Note: Read the exact current HTML from the file before editing — the SVG paths above are illustrative. Preserve the exact existing markup inside the conditional.

**Step 2: Gate date picker options by max_data_months**

Add a hidden input to pass the max months to JS:

```html
<input type="hidden" id="max_data_months" value="{{.max_data_months}}">
```

Place this near the existing `period_months` hidden input.

**Step 3: Run tests and commit**

Run: `make qualify`
Expected: PASS

```bash
git add pkg/server/templates/home.html
git commit -S -m "feat: gate PDF button and pass max data months to frontend"
```

---

## Task 7: Clamp Date Picker in Frontend JS

**Files:**
- Modify: `pkg/server/static/js/app.js`

**Context:** `updatePeriodOptions()` (line 1897) builds the `#period-select` dropdown from available data range. We need to also cap it by the plan's max months.

**Step 1: Add plan-based filtering in updatePeriodOptions**

After the dropdown options are built from min-date (around line 1912-1943), filter out options that exceed `max_data_months`:

```javascript
// Inside updatePeriodOptions, after building option list from min-date:
const maxDataMonths = parseInt(document.getElementById('max_data_months')?.value || '0', 10);
if (maxDataMonths > 0) {
    // Remove options exceeding plan limit
    const options = periodSelect.querySelectorAll('option');
    options.forEach(opt => {
        if (parseInt(opt.value, 10) > maxDataMonths) {
            opt.remove();
        }
    });
}
```

Insert this after the existing loop that populates `#period-select` options but before the selected value is set.

**Step 2: Run and verify manually**

Run: `make server`
Verify: Date picker only shows options up to the plan's max months.

**Step 3: Commit**

```bash
git add pkg/server/static/js/app.js
git commit -S -m "feat: cap date picker options by plan data retention limit"
```

---

## Task 8: CSV/ZIP Export Handler

**Files:**
- Modify: `pkg/server/data.go` (add handler)
- Modify: `pkg/server/server.go` (register route)

**Context:** New `GET /data/export/csv` endpoint. Accepts `?o=org&r=repo` (optional, defaults to all repos) and `?m=months` (clamped by plan). Returns a ZIP archive with CSVs per repo. Gated by `CSVExport` plan flag.

**Step 1: Add csvExportHandler**

In `pkg/server/data.go`, add the handler. It needs access to several existing store methods to gather data per repo. The handler:

1. Checks plan allows CSV export
2. Parses params (months already clamped by `parseInsightParams`)
3. Queries repos from store (or uses specified org/repo)
4. For each repo: queries events, developers, insights, reputation
5. Streams ZIP response

```go
func csvExportHandler(defaultStore data.Store, db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tn := middleware.TenantFromContext(r.Context())
		if tn == nil {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}

		limits, ok := plan.Get(tn.Plan)
		if !ok || !limits.CSVExport {
			writeError(w, http.StatusForbidden, "CSV export is not available on your plan")
			return
		}

		s := storeFromRequest(r, defaultStore)
		p := parseInsightParams(r)
		ctx := r.Context()

		// Get repos to export
		repos, err := tenant.ListActiveRepos(ctx, db, tn.ID)
		if err != nil {
			slog.Error("listing repos for export", "error", err)
			writeError(w, http.StatusInternalServerError, "error listing repos")
			return
		}

		// Filter to specific repo if requested
		if p.org != nil && p.repo != nil {
			filtered := make([]tenant.OrgRepo, 0, 1)
			for _, rp := range repos {
				if rp.Org == *p.org && rp.Repo == *p.repo {
					filtered = append(filtered, rp)
					break
				}
			}
			repos = filtered
		}

		if len(repos) == 0 {
			writeError(w, http.StatusNotFound, "no repos to export")
			return
		}

		today := time.Now().UTC().Format("2006-01-02")
		filename := fmt.Sprintf("devpulse-export-%s.zip", today)

		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))

		zw := zip.NewWriter(w)
		defer zw.Close()

		for _, rp := range repos {
			prefix := fmt.Sprintf("export-%s/%s-%s/", today, rp.Org, rp.Repo)
			writeRepoCSVs(ctx, zw, s, rp.Org, rp.Repo, p.months, prefix)
		}
	}
}
```

The `writeRepoCSVs` helper writes individual CSV files into the ZIP for each dataset type. This is ~80-100 lines using `encoding/csv` to write rows from existing store query results. Exact implementation depends on the store method return types — read each store method used (`GetEventSearch`, `GetDeveloperSearch`, insight getters, reputation getter) before implementing.

**Important:** Check `tenant.ListActiveRepos` exists. If not, add a query function that returns `[]OrgRepo` for a tenant ID (should be straightforward — query `tenant_repo WHERE tenant_id = $1 AND active = TRUE`).

**Step 2: Register route**

In `pkg/server/server.go`, after the existing data routes (line 222):

```go
mux.Handle("GET /data/export/csv", scopedWrap(csvExportHandler(store, db)))
```

**Step 3: Add imports**

In `data.go`, add:
```go
"archive/zip"
"encoding/csv"
"time"
```

**Step 4: Run tests and commit**

Run: `make qualify`
Expected: PASS

```bash
git add pkg/server/data.go pkg/server/server.go
git commit -S -m "feat: add CSV/ZIP export endpoint gated by plan"
```

---

## Task 9: Export Tab in Dashboard Template

**Files:**
- Modify: `pkg/server/templates/home.html`
- Modify: `pkg/server/static/js/app.js`

**Context:** Add an Export section/tab in the dashboard for Starter+ users. Contains PDF button (moved from toolbar) and CSV download button (Pro+). Both include a repo selector.

**Step 1: Add export section to home.html**

Read `home.html` fully before modifying. Add an export panel (visible to Starter+) that includes:
- Repo multi-select dropdown (populated from existing `#repo-select` data)
- PDF download button (Starter+): `{{if .pdf_export}}`
- CSV download button (Pro+): `{{if .csv_export}}`

Keep the existing PDF button in the toolbar as well (gated by `{{if .pdf_export}}` from Task 6) — users expect it in both places.

**Step 2: Add CSV download JS**

In `app.js`, add a function that calls `/data/export/csv` with selected repos and months, then triggers a browser download:

```javascript
function downloadCSV() {
    const months = document.getElementById('period_months').value;
    const org = searchCriteria.org || '';
    const repo = searchCriteria.repo || '';
    let url = `/data/export/csv?m=${months}`;
    if (org && repo) {
        url += `&o=${org}&r=${repo}`;
    }
    window.location.href = url;
}
```

Wire the CSV button's click handler to `downloadCSV()`.

**Step 3: Run and verify manually**

Run: `make server`
Verify: Export section shows for non-Free plans, PDF/CSV buttons appear per plan.

**Step 4: Commit**

```bash
git add pkg/server/templates/home.html pkg/server/static/js/app.js
git commit -S -m "feat: add export tab with PDF and CSV download buttons"
```

---

## Task 10: Admin Invite Endpoint

**Files:**
- Modify: `pkg/admin/admin.go` (add handler + route)
- Modify: `pkg/admin/types.go` (add request/response types)
- Create: `tools/tenant-invite`

**Context:** New `POST /invite` endpoint that creates a tenant row by GitHub username and sets their plan. Resolves GitHub user ID via the GitHub API (unauthenticated `GET /users/{username}` — no token needed, 60 req/hr limit is fine for admin use).

**Step 1: Add types**

In `pkg/admin/types.go`, add:

```go
type inviteRequest struct {
	Username string `json:"username"`
	Plan     string `json:"plan"`
}

type inviteResponse struct {
	Username         string `json:"username"`
	GitHubID         int64  `json:"github_id"`
	Plan             string `json:"plan"`
	MaxRepos         int    `json:"max_repos"`
	MaxEventsPerWeek int    `json:"max_events_per_week"`
}
```

**Step 2: Add handler**

In `pkg/admin/admin.go`, add `handleInvite(db)`:

```go
const insertMinimalTenantSQL = `
	INSERT INTO tenant (github_id, username)
	VALUES ($1, $2)
	ON CONFLICT (github_id) DO UPDATE SET updated_at = NOW()
	RETURNING id`

func handleInvite(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyLen)
		var req inviteRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}

		if req.Username == "" {
			http.Error(w, "username is required", http.StatusBadRequest)
			return
		}

		limits, ok := plan.Get(req.Plan)
		if !ok {
			http.Error(w, fmt.Sprintf(
				"invalid plan: %s (must be free, starter, pro, or enterprise)", req.Plan,
			), http.StatusBadRequest)
			return
		}

		// Resolve GitHub user ID
		ghID, err := resolveGitHubUserID(r.Context(), req.Username)
		if err != nil {
			http.Error(w, fmt.Sprintf("GitHub user not found: %s", req.Username), http.StatusNotFound)
			return
		}

		// Insert minimal tenant row
		var tenantID string
		err = db.QueryRowContext(r.Context(), insertMinimalTenantSQL, ghID, req.Username).Scan(&tenantID)
		if err != nil {
			slog.Error("inserting tenant", "error", err)
			http.Error(w, "error creating tenant", http.StatusInternalServerError)
			return
		}

		// Set plan
		if err := tenant.UpdatePlan(r.Context(), db, tenantID, req.Plan, limits.MaxRepos, limits.MaxEventsPerWeek); err != nil {
			slog.Error("setting plan", "error", err)
			http.Error(w, "error setting plan", http.StatusInternalServerError)
			return
		}

		slog.Info("tenant invited",
			"username", req.Username,
			"github_id", ghID,
			"plan", req.Plan,
		)

		writeJSON(w, inviteResponse{
			Username:         req.Username,
			GitHubID:         ghID,
			Plan:             req.Plan,
			MaxRepos:         limits.MaxRepos,
			MaxEventsPerWeek: limits.MaxEventsPerWeek,
		})
	}
}
```

**Step 3: Add GitHub user resolution**

In `pkg/admin/admin.go`, add:

```go
func resolveGitHubUserID(ctx context.Context, username string) (int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		fmt.Sprintf("https://api.github.com/users/%s", username), nil)
	if err != nil {
		return 0, fmt.Errorf("building request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("calling GitHub API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("GitHub API returned %d", resp.StatusCode)
	}

	var ghUser struct {
		ID int64 `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&ghUser); err != nil {
		return 0, fmt.Errorf("decoding response: %w", err)
	}

	return ghUser.ID, nil
}
```

**Step 4: Register route**

In `pkg/admin/admin.go`, after line 83:

```go
mux.HandleFunc("POST /invite", handleInvite(db))
```

**Step 5: Create tools/tenant-invite script**

Create `tools/tenant-invite` (mirror `tools/tenant-upgrade` pattern):

```bash
#!/bin/bash
set -euo pipefail

DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
. "${DIR}/common"

has_tools gcloud terraform curl

usage() {
    echo "Usage: $0 <github-username> <plan>"
    echo ""
    echo "Pre-creates a tenant with the given plan."
    echo "When the user logs in via OAuth, their plan is preserved."
    echo ""
    echo "Plans:"
    echo "  free        1 repo, 500 events/week"
    echo "  starter     5 repos, 2,500 events/week"
    echo "  pro         25 repos, 15,000 events/week"
    echo "  enterprise  unlimited"
    exit 1
}

[[ $# -ne 2 ]] && usage

USERNAME="$1"
PLAN="$2"

case "$PLAN" in
    free|starter|pro|enterprise) ;;
    *) err "Unknown plan: $PLAN (must be free, starter, pro, or enterprise)" ;;
esac

msg "Fetching admin service URL..."
ADMIN_URL="$(cd "${REPO_ROOT}/infra/saas" && terraform output -raw admin_url 2>/dev/null)" \
    || err "Failed to get admin URL. Run 'terraform apply' in infra/saas first."

msg "Getting identity token..."
TOKEN="$(gcloud auth print-identity-token 2>/dev/null)" \
    || err "Failed to get identity token. Run 'gcloud auth login' first."

msg "Inviting ${USERNAME} with plan ${PLAN}..."
RESPONSE="$(curl -s -w "\n%{http_code}" \
    -H "Authorization: Bearer ${TOKEN}" \
    -H "Content-Type: application/json" \
    -d "{\"username\":\"${USERNAME}\",\"plan\":\"${PLAN}\"}" \
    "${ADMIN_URL}/invite")"

HTTP_CODE="$(echo "${RESPONSE}" | tail -1)"
BODY="$(echo "${RESPONSE}" | head -1)"

if [[ "${HTTP_CODE}" == "200" ]]; then
    msg "Success: ${BODY}"
else
    err "Failed (HTTP ${HTTP_CODE}): ${BODY}"
fi
```

Make executable: `chmod +x tools/tenant-invite`

**Step 6: Run tests and commit**

Run: `make qualify`
Expected: PASS

```bash
git add pkg/admin/admin.go pkg/admin/types.go tools/tenant-invite
git commit -S -m "feat: add admin invite endpoint for pre-creating tenants"
```

---

## Task 11: Final Qualify and Integration Verification

**Step 1: Run full qualification**

Run: `make qualify`
Expected: All tests pass, no lint errors, no vulnerabilities.

**Step 2: Manual verification checklist**

With `make server` running:

- [ ] Free plan: PDF button hidden, date picker capped at 3 months
- [ ] Free plan: `/data/export/csv` returns 403
- [ ] Free plan: `/data/insights/generated` returns empty (no insights generated)
- [ ] Starter plan: PDF button visible, date picker capped at 12 months
- [ ] Starter plan: AI insights returned, but action items stripped
- [ ] Pro plan: PDF + CSV buttons visible, date picker up to 36 months
- [ ] Pro plan: AI insights + action items both returned
- [ ] Admin invite: `./tools/tenant-invite <username> pro` creates tenant with correct plan
- [ ] Invited user: OAuth login preserves pre-set plan

**Step 3: Final commit if any fixups needed**

```bash
git add -A
git commit -S -m "fix: address integration test feedback"
```

---

## Summary of Changes by File

| File | Change |
|------|--------|
| `pkg/plan/plan.go` | Add Starter, extend Limits struct, update All map |
| `pkg/plan/plan_test.go` | Update tests for new plans and fields |
| `pkg/importer/importer.go` | Gate deep reputation + AI by plan |
| `pkg/server/data.go` | Clamp months, strip AI actions, add CSV export handler |
| `pkg/server/server.go` | Pass plan flags to dashboard, register CSV route |
| `pkg/server/templates/home.html` | Conditional PDF button, max months hidden input, export section |
| `pkg/server/static/js/app.js` | Cap date picker, add CSV download function |
| `pkg/admin/admin.go` | Add invite handler + GitHub user resolution |
| `pkg/admin/types.go` | Add invite request/response types |
| `tools/tenant-upgrade` | Add starter to usage and case |
| `tools/tenant-invite` | New script (mirrors tenant-upgrade) |

## Dependencies Between Tasks

```
Task 1 (plan constants) ──┬── Task 2 (importer gating)
                          ├── Task 3 (date clamp)
                          ├── Task 4 (AI display gate)
                          ├── Task 5 (dashboard data) ──── Task 6 (PDF template) ──── Task 7 (JS date picker)
                          ├── Task 8 (CSV handler) ──── Task 9 (export tab)
                          └── Task 10 (admin invite)
```

Tasks 2, 3, 4, 5, 8, 10 can all proceed in parallel after Task 1. Tasks 6-7 depend on 5. Task 9 depends on 8.
