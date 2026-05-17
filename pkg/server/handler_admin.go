package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/thingzio/devpulse/pkg/config"
	"github.com/thingzio/devpulse/pkg/digest"
	"github.com/thingzio/devpulse/pkg/middleware"
	"github.com/thingzio/devpulse/pkg/net"
	"github.com/thingzio/devpulse/pkg/plan"
	"github.com/thingzio/devpulse/pkg/tenant"
)

// Template data structs for admin pages.

const pageTitleTenants = "Tenants"

type adminDashboardData struct {
	Title     string
	Summary   summaryResponse
	CSRFToken string
}

type adminTenantsData struct {
	Title      string
	Tenants    []tenantSummary
	Plans      map[string]plan.Limits
	CSRFToken  string
	Search     string
	Page       int
	TotalPages int
	Total      int
}

type adminTenantDetailData struct {
	Title     string
	Detail    tenantDetail
	Plans     map[string]plan.Limits
	CSRFToken string
}

type adminTokensData struct {
	Title            string
	Tokens           []tokenStatus
	ErrorRepos       []importErrorRepo
	NoInstallTenants []noInstallTenant
	CSRFToken        string
}

type importErrorRepo struct {
	Org       string
	Repo      string
	Errors    int
	LastError string
	Username  string
}

type noInstallTenant struct {
	Username    string
	Plan        string
	ActiveRepos int
}

type adminMetricsData struct {
	Title      string
	Days       int
	DayOptions []int
	Metrics    string
	Analysis   string
}

// GET /admin — dashboard with platform summary.
func adminDashboardHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		summary, err := collectSummary(r.Context(), db)
		if err != nil {
			slog.Error("collecting admin summary", "error", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		csrfToken := middleware.EnsureCSRFToken(w, r)
		renderTemplate(w, "admin.html", adminDashboardData{
			Title:     "Admin",
			Summary:   summary,
			CSRFToken: csrfToken,
		})
	}
}

// GET /admin/tenants — list all tenants with server-side search and pagination.
func adminTenantsHandler(db *sql.DB) http.HandlerFunc {
	const pageSize = 10

	return func(w http.ResponseWriter, r *http.Request) {
		search := r.URL.Query().Get("q")
		page := 1
		if p := r.URL.Query().Get("page"); p != "" {
			if v, err := strconv.Atoi(p); err == nil && v > 0 {
				page = v
			}
		}
		offset := (page - 1) * pageSize

		result, err := tenant.ListTenantSummariesPaged(r.Context(), db, search, pageSize, offset)
		if err != nil {
			slog.Error("listing tenants", "error", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		out := make([]tenantSummary, len(result.Tenants))
		for i, t := range result.Tenants {
			out[i] = tenantSummary{
				Username:         t.Username,
				Email:            t.Email,
				Name:             t.Name,
				Plan:             t.Plan,
				Status:           t.Status,
				MaxRepos:         t.MaxRepos,
				MaxEventsPerWeek: t.MaxEventsPerWeek,
				CreatedAt:        t.CreatedAt.Format("2006-01-02"),
			}
			if t.LastSignIn != nil {
				out[i].LastSignIn = t.LastSignIn.Format("2006-01-02 15:04")
			}
		}

		totalPages := (result.Total + pageSize - 1) / pageSize
		if totalPages < 1 {
			totalPages = 1
		}

		csrfToken := middleware.EnsureCSRFToken(w, r)
		renderTemplate(w, "admin_tenants.html", adminTenantsData{
			Title:      pageTitleTenants,
			Tenants:    out,
			Plans:      plan.All,
			CSRFToken:  csrfToken,
			Search:     search,
			Page:       page,
			TotalPages: totalPages,
			Total:      result.Total,
		})
	}
}

// GET /admin/tenant/{username} — tenant detail with repos.
func adminTenantDetailHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username := r.PathValue("username")
		if username == "" {
			http.Error(w, "username required", http.StatusBadRequest)
			return
		}

		qctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()

		td, err := tenant.GetTenantDetailByUsername(qctx, db, username)
		if err != nil {
			slog.Debug("tenant not found", "username", username, "error", err)
			http.Error(w, "tenant not found", http.StatusNotFound)
			return
		}

		out := tenantDetail{
			Username:         td.Username,
			Email:            td.Email,
			Name:             td.Name,
			Company:          td.Company,
			Location:         td.Location,
			Bio:              td.Bio,
			Plan:             td.Plan,
			Status:           td.Status,
			MaxRepos:         td.MaxRepos,
			MaxEventsPerWeek: td.MaxEventsPerWeek,
			CreatedAt:        td.CreatedAt.Format("2006-01-02"),
		}
		if td.LastSignIn != nil {
			out.LastSignIn = td.LastSignIn.Format("2006-01-02 15:04")
		}
		if td.DigestLastSentAt != nil {
			out.DigestLastSentAt = td.DigestLastSentAt.Format("2006-01-02 15:04")
		}

		since := time.Now().UTC().AddDate(0, 0, -180).Format("2006-01-02")
		weekStart := tenant.StartOfWeek().Format("2006-01-02")
		backfillSince := time.Now().UTC().AddDate(0, 0, -config.BackfillMaxDays()).Format("2006-01-02")

		repos, err := tenant.GetTenantRepoDetails(qctx, db, td.ID, since, weekStart, backfillSince)
		if err != nil {
			slog.Error("querying tenant repos", "error", err)
			http.Error(w, "error querying repos", http.StatusInternalServerError)
			return
		}

		for _, rd := range repos {
			d := repoDetail{
				Name:           rd.Org + "/" + rd.Repo,
				Events:         rd.Events,
				WeeklyEvents:   rd.WeeklyEvents,
				LastImport:     rd.LastImport,
				PRTotal:        rd.PRTotal,
				PRMissingSize:  rd.PRMissingSize,
				Contributors:   rd.Contributors,
				Scored:         rd.Scored,
				BackfillDays:   rd.BackfillDays,
				BackfillTarget: rd.BackfillTarget,
			}
			if out.MaxEventsPerWeek > 0 {
				d.WeeklyPct = float64(rd.WeeklyEvents) / float64(out.MaxEventsPerWeek) * 100
			}
			out.Repos = append(out.Repos, d)
		}

		csrfToken := middleware.EnsureCSRFToken(w, r)
		renderTemplate(w, "admin_tenant.html", adminTenantDetailData{
			Title:     "Tenant: " + username,
			Detail:    out,
			Plans:     plan.All,
			CSRFToken: csrfToken,
		})
	}
}

// GET /admin/tokens — GitHub App installation token status.
func adminTokensHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ghAppConfig, err := tenant.LoadGitHubAppConfig()
		if err != nil {
			slog.Error("loading github app config", "error", err)
			http.Error(w, "github app config not available", http.StatusInternalServerError)
			return
		}

		tenants, err := tenant.GetActiveTenants(r.Context(), db)
		if err != nil {
			slog.Error("getting tenants for token status", "error", err)
			http.Error(w, "error listing tenants", http.StatusInternalServerError)
			return
		}

		seen := make(map[int64]bool)
		var results []tokenStatus

		for _, tn := range tenants {
			installs, instErr := tenant.GetActiveInstallations(r.Context(), db, tn.ID, ghAppConfig.AppID)
			if instErr != nil || len(installs) == 0 {
				continue
			}
			for _, inst := range installs {
				if seen[inst.ID] {
					continue
				}
				seen[inst.ID] = true

				tok, mintErr := tenant.MintInstallationToken(r.Context(), ghAppConfig, inst.ID)
				if mintErr != nil {
					results = append(results, tokenStatus{
						Login:          inst.Login,
						InstallationID: inst.ID,
						Error:          mintErr.Error(),
					})
					continue
				}

				ts := checkGitHubRateLimit(r.Context(), tok.Token)
				ts.Login = inst.Login
				ts.InstallationID = inst.ID
				results = append(results, ts)
			}
		}

		ctx := r.Context()

		errorRepos, err := tenant.ListImportErrorRepos(ctx, db)
		if err != nil {
			slog.Error("listing import error repos", "error", err)
		}

		var errRepos []importErrorRepo
		for _, r := range errorRepos {
			errRepos = append(errRepos, importErrorRepo{
				Org:       r.Org,
				Repo:      r.Repo,
				Errors:    r.Errors,
				LastError: r.LastError,
				Username:  r.Username,
			})
		}

		noInstall, err := tenant.ListTenantsWithoutInstall(ctx, db)
		if err != nil {
			slog.Error("listing tenants without install", "error", err)
		}

		var noInstallOut []noInstallTenant
		for _, t := range noInstall {
			noInstallOut = append(noInstallOut, noInstallTenant{
				Username:    t.Username,
				Plan:        t.Plan,
				ActiveRepos: t.ActiveRepos,
			})
		}

		csrfToken := middleware.EnsureCSRFToken(w, r)

		renderTemplate(w, "admin_tokens.html", adminTokensData{
			Title:            "Token Status",
			Tokens:           results,
			ErrorRepos:       errRepos,
			NoInstallTenants: noInstallOut,
			CSRFToken:        csrfToken,
		})
	}
}

// checkGitHubRateLimit calls the GitHub rate_limit API and returns a tokenStatus.
func checkGitHubRateLimit(ctx context.Context, token string) tokenStatus {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/rate_limit", nil)
	if err != nil {
		slog.Error("creating rate limit request", "error", err)
		return tokenStatus{Error: "request error"}
	}
	net.SetGitHubHeaders(req, token)

	resp, err := net.GitHubClient.Do(req)
	if err != nil {
		slog.Error("calling rate limit API", "error", err)
		return tokenStatus{Error: "rate limit check failed"}
	}
	defer resp.Body.Close()

	var rl struct {
		Resources struct {
			Core struct {
				Limit     int   `json:"limit"`
				Remaining int   `json:"remaining"`
				Reset     int64 `json:"reset"`
			} `json:"core"`
		} `json:"resources"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rl); err != nil {
		slog.Error("decoding rate limit response", "error", err)
		return tokenStatus{Error: "response decode error"}
	}

	core := rl.Resources.Core
	used := core.Limit - core.Remaining
	resetAt := time.Unix(core.Reset, 0).UTC().Format(time.RFC3339)

	return tokenStatus{
		Limit:     core.Limit,
		Used:      used,
		Remaining: core.Remaining,
		ResetAt:   resetAt,
	}
}

// GET /admin/metrics — GCP metrics review with AI analysis.
func adminMetricsHandler(mcfg *metricsConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		days := defaultDays
		if d := r.URL.Query().Get("days"); d != "" {
			if v, err := strconv.Atoi(d); err == nil && v > 0 && v <= maxDays {
				days = v
			}
		}

		token, err := gcpAccessToken(r.Context())
		if err != nil {
			slog.Error("failed to get GCP credentials", "error", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		metrics := collectAllMetrics(r.Context(), mcfg, token, days)

		var analysis string
		if mcfg.anthropicKey != "" {
			var err2 error
			analysis, err2 = analyzeMetrics(r.Context(), mcfg, metrics, formatText)
			if err2 != nil {
				slog.Error("failed to analyze metrics", "error", err2)
			}
		}

		renderTemplate(w, "admin_metrics.html", adminMetricsData{
			Title:      "Metrics Review",
			Days:       days,
			DayOptions: metricsDayOptions,
			Metrics:    metrics,
			Analysis:   analysis,
		})
	}
}

// POST /admin/tenant/{username}/plan — update tenant plan.
func adminUpdatePlanHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)

		username := r.PathValue("username")
		if username == "" {
			http.Error(w, "username required", http.StatusBadRequest)
			return
		}

		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		if !middleware.ValidateCSRFFromRequest(r) {
			http.Error(w, "invalid CSRF token", http.StatusForbidden)
			return
		}

		planName := r.FormValue("plan")
		limits, ok := plan.Get(planName)
		if !ok {
			http.Error(w, fmt.Sprintf("invalid plan: %s", planName), http.StatusBadRequest)
			return
		}

		ctx := r.Context()
		tenantID, err := tenant.GetTenantIDByUsername(ctx, db, username)
		if err != nil {
			slog.Debug("tenant not found for upgrade", "username", username, "error", err)
			http.Error(w, "tenant not found", http.StatusNotFound)
			return
		}

		if err := tenant.UpdatePlan(ctx, db, tenantID, planName, limits.MaxRepos, limits.MaxEventsPerWeek); err != nil {
			slog.Error("updating plan", "error", err)
			http.Error(w, "error updating plan", http.StatusInternalServerError)
			return
		}

		if err := tenant.ClearUpgradeRequest(ctx, db, tenantID); err != nil {
			slog.Warn("clearing upgrade request", "error", err)
		}

		middleware.AdminAuditLog(ctx, "update_plan",
			r.URL.Path, r.RemoteAddr,
			fmt.Sprintf("username=%s plan=%s", username, planName))

		http.Redirect(w, r, "/admin/tenant/"+url.PathEscape(username), http.StatusSeeOther)
	}
}

// POST /admin/tenant/{username}/status — change tenant status.
func adminUpdateStatusHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username := r.PathValue("username")
		if username == "" {
			http.Error(w, "username required", http.StatusBadRequest)
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		if !middleware.ValidateCSRFFromRequest(r) {
			http.Error(w, "invalid CSRF token", http.StatusForbidden)
			return
		}

		status := r.FormValue("status")
		if status != tenant.StatusActive && status != tenant.StatusSuspended {
			http.Error(w, fmt.Sprintf("invalid status: %s", status), http.StatusBadRequest)
			return
		}

		ctx := r.Context()
		tenantID, err := tenant.GetTenantIDByUsername(ctx, db, username)
		if err != nil {
			http.Error(w, "tenant not found", http.StatusNotFound)
			return
		}

		if err := tenant.UpdateStatus(ctx, db, tenantID, status); err != nil {
			slog.Error("updating status", "error", err)
			http.Error(w, "error updating status", http.StatusInternalServerError)
			return
		}

		middleware.AdminAuditLog(ctx, "update_status",
			r.URL.Path, r.RemoteAddr,
			fmt.Sprintf("username=%s status=%s", username, status))

		http.Redirect(w, r, "/admin/tenant/"+url.PathEscape(username), http.StatusSeeOther)
	}
}

// POST /admin/invite — invite a new tenant.
func adminInviteHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)

		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		if !middleware.ValidateCSRFFromRequest(r) {
			http.Error(w, "invalid CSRF token", http.StatusForbidden)
			return
		}

		username := r.FormValue("username")
		if username == "" {
			http.Error(w, "username required", http.StatusBadRequest)
			return
		}

		planName := r.FormValue("plan")
		limits, ok := plan.Get(planName)
		if !ok {
			http.Error(w, fmt.Sprintf("invalid plan: %s", planName), http.StatusBadRequest)
			return
		}

		ctx := r.Context()
		ghID, err := resolveGitHubUserID(ctx, username)
		if err != nil {
			slog.Error("resolving GitHub user", "username", username, "error", err)
			http.Error(w, "GitHub user not found", http.StatusNotFound)
			return
		}

		tenantID, err := tenant.InsertMinimalTenant(ctx, db, ghID, username)
		if err != nil {
			slog.Error("inserting tenant", "error", err)
			http.Error(w, "error creating tenant", http.StatusInternalServerError)
			return
		}

		if err := tenant.UpdatePlan(ctx, db, tenantID, planName, limits.MaxRepos, limits.MaxEventsPerWeek); err != nil {
			slog.Error("setting plan", "error", err)
			http.Error(w, "error setting plan", http.StatusInternalServerError)
			return
		}

		middleware.AdminAuditLog(ctx, "invite_tenant",
			r.URL.Path, r.RemoteAddr,
			fmt.Sprintf("username=%s github_id=%d plan=%s", username, ghID, planName))

		http.Redirect(w, r, "/admin/tenants", http.StatusSeeOther)
	}
}

// POST /admin/tenant/{username}/reset-errors — reset import errors for a repo.
func adminResetErrorsHandler(db *sql.DB) http.HandlerFunc {
	return adminRepoActionHandler(db, "reset_errors", tenant.ResetImportErrorsByRepo)
}

// POST /admin/tenant/{username}/hard-reset — hard reset a repo.
func adminHardResetHandler(db *sql.DB) http.HandlerFunc {
	return adminRepoActionHandler(db, "hard_reset", tenant.HardResetRepo)
}

// adminRepoActionHandler is a shared handler for repo-level admin actions (reset errors, hard reset).
func adminRepoActionHandler(db *sql.DB, action string, fn func(ctx context.Context, db *sql.DB, org, repo string) (int64, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)

		username := r.PathValue("username")
		if username == "" {
			http.Error(w, "username required", http.StatusBadRequest)
			return
		}

		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		if !middleware.ValidateCSRFFromRequest(r) {
			http.Error(w, "invalid CSRF token", http.StatusForbidden)
			return
		}

		org := r.FormValue("org")
		repo := r.FormValue("repo")
		if org == "" || repo == "" {
			http.Error(w, "org and repo required", http.StatusBadRequest)
			return
		}

		ctx := r.Context()
		count, err := fn(ctx, db, org, repo)
		if err != nil {
			slog.Error("admin repo action", "action", action, "org", org, "repo", repo, "error", err)
			http.Error(w, fmt.Sprintf("error: %s", action), http.StatusInternalServerError)
			return
		}

		middleware.AdminAuditLog(ctx, action,
			r.URL.Path, r.RemoteAddr,
			fmt.Sprintf("org=%s repo=%s rows=%d", org, repo, count))

		http.Redirect(w, r, "/admin/tenant/"+url.PathEscape(username), http.StatusSeeOther)
	}
}

// GET /admin/tokens/quota-history — JSON time-series of token quota samples.
func adminTokenQuotaHistoryHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		hours := 24
		if h := r.URL.Query().Get("hours"); h != "" {
			if v, err := strconv.Atoi(h); err == nil && v > 0 && v <= 720 {
				hours = v
			}
		}

		since := time.Now().UTC().Add(-time.Duration(hours) * time.Hour)
		samples, err := getTokenQuotaSamples(r.Context(), db, since)
		if err != nil {
			slog.Error("querying quota history", "error", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		writeJSON(w, http.StatusOK, samples)
	}
}

// POST /admin/digest/send — send digest email to the current admin user now.
func adminDigestSendHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 1024)

		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		if !middleware.ValidateCSRFFromRequest(r) {
			http.Error(w, "invalid CSRF token", http.StatusForbidden)
			return
		}

		tn := middleware.TenantFromContext(r.Context())
		if tn == nil {
			http.Error(w, "no tenant in context", http.StatusUnauthorized)
			return
		}

		cfg := digest.NewConfigFromEnv()
		if cfg == nil {
			http.Error(w, "digest not configured (missing env vars)", http.StatusServiceUnavailable)
			return
		}

		cfg.TestUsername = tn.Username

		if err := digest.Run(r.Context(), db, cfg); err != nil {
			slog.Error("admin digest send", "username", tn.Username, "error", err)
			http.Error(w, "digest send failed", http.StatusInternalServerError)
			return
		}

		middleware.AdminAuditLog(r.Context(), "digest_send",
			r.URL.Path, r.RemoteAddr,
			fmt.Sprintf("username=%s", tn.Username))

		http.Redirect(w, r, "/admin", http.StatusSeeOther)
	}
}

// resolveGitHubUserID resolves a GitHub username to their numeric user ID.
func resolveGitHubUserID(ctx context.Context, username string) (int64, error) {
	apiURL := "https://api.github.com/users/" + url.PathEscape(username)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return 0, fmt.Errorf("building request: %w", err)
	}
	req.Header.Set("Accept", net.GitHubAccept)

	resp, err := net.GitHubClient.Do(req)
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
