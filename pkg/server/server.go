package server

import (
	"bytes"
	"context"
	"crypto/subtle"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/thingzio/devpulse/pkg/config"
	"github.com/thingzio/devpulse/pkg/data"
	"github.com/thingzio/devpulse/pkg/data/postgres"
	"github.com/thingzio/devpulse/pkg/digest"
	"github.com/thingzio/devpulse/pkg/middleware"
	"github.com/thingzio/devpulse/pkg/net"
	"github.com/thingzio/devpulse/pkg/oauth"
	"github.com/thingzio/devpulse/pkg/plan"
	"github.com/thingzio/devpulse/pkg/tenant"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static/*
var staticFS embed.FS

var pageTemplates map[string]*template.Template

// serverOpts holds version info set at Run() time, read by template funcs.
var serverOpts Options

var templateFuncs = template.FuncMap{
	"appVersion":   func() string { return serverOpts.Version },
	"appCommit":    func() string { return serverOpts.Commit },
	"appBuildDate": func() string { return serverOpts.Date },
	"pctOf": func(part, total int) string {
		if total == 0 {
			return "0%"
		}
		return fmt.Sprintf("%d%%", part*100/total)
	},
	"add": func(a, b int) int { return a + b },
	"sub": func(a, b int) int { return a - b },
	"mul": func(a, b int) int { return a * b },
	"div": func(a, b int) int {
		if b == 0 {
			return 0
		}
		return a / b
	},
	"sumTokenLimit": func(tokens []tokenStatus) int {
		var total int
		for _, t := range tokens {
			total += t.Limit
		}
		return total
	},
	"sumTokenUsed": func(tokens []tokenStatus) int {
		var total int
		for _, t := range tokens {
			total += t.Used
		}
		return total
	},
	"tokenHasIssue": func(t tokenStatus) bool {
		if t.Error != "" {
			return true
		}
		return t.Limit > 0 && t.Used*100/t.Limit >= 85
	},
	"filterTokenIssues": func(tokens []tokenStatus) []tokenStatus {
		var out []tokenStatus
		for _, t := range tokens {
			if t.Error != "" || (t.Limit > 0 && t.Used*100/t.Limit >= 85) {
				out = append(out, t)
			}
		}
		return out
	},
	"comma": func(n int) string {
		if n == 0 {
			return "Unlimited"
		}
		if n < 1000 {
			return fmt.Sprintf("%d", n)
		}
		if n < 1000000 {
			return fmt.Sprintf("%d,%03d", n/1000, n%1000)
		}
		return fmt.Sprintf("%d,%03d,%03d", n/1000000, (n/1000)%1000, n%1000)
	},
	// deltaInt formats a statsDelta int field with color. Positive = green.
	"deltaInt": func(d *statsDelta, field string) template.HTML {
		return formatDeltaInt(d, field, false)
	},
	// deltaIntInv is like deltaInt but inverts color (positive = red, for error counts).
	"deltaIntInv": func(d *statsDelta, field string) template.HTML {
		return formatDeltaInt(d, field, true)
	},
	// deltaInt64 formats a statsDelta int64 field with color.
	"deltaInt64": formatDeltaInt64,
	// splitOrgRepo splits "org/repo" into [org, repo].
	"splitOrgRepo": func(s string) [2]string {
		if org, repo, ok := strings.Cut(s, "/"); ok {
			return [2]string{org, repo}
		}
		return [2]string{s, ""}
	},
}

const (
	deltaEmDash    = "&mdash;"
	deltaColorNone = "inherit"
	deltaColorGood = "green"
	deltaColorBad  = "red"
)

// formatDeltaInt formats an int delta field from statsDelta with colored HTML.
// When invert is true, positive values are red (used for error-type metrics).
func formatDeltaInt(d *statsDelta, field string, invert bool) template.HTML {
	if d == nil {
		return deltaEmDash
	}

	var val *int
	var pct *float64

	switch field {
	case "Tenants":
		val, pct = d.Tenants, d.TenantsPct
	case "Repos":
		val, pct = d.Repos, d.ReposPct
	case "Contributors":
		val, pct = d.Contributors, d.ContribPct
	case "Installations":
		val, pct = d.Installations, d.InstallPct
	case "ReposWithErrors":
		val, pct = d.ReposWithErrors, d.ErrorsPct
	default:
		return deltaEmDash
	}

	return formatIntDelta(val, pct, invert)
}

// formatDeltaInt64 formats an int64 delta field from statsDelta with colored HTML.
func formatDeltaInt64(d *statsDelta, _ string) template.HTML {
	if d == nil || d.Events == nil {
		return deltaEmDash
	}
	return formatInt64Delta(*d.Events, d.EventsPct)
}

func formatIntDelta(val *int, pct *float64, invert bool) template.HTML {
	if val == nil {
		return deltaEmDash
	}

	v := *val
	color := deltaColor(int64(v), invert)

	sign := ""
	if v > 0 {
		sign = "+"
	}

	s := fmt.Sprintf("%s%d", sign, v)
	if pct != nil {
		s += fmt.Sprintf(" (%.1f%%)", *pct)
	}

	return template.HTML(fmt.Sprintf(`<span style="color:%s">%s</span>`, color, s)) //nolint:gosec // computed values only, no user input
}

func formatInt64Delta(v int64, pct *float64) template.HTML {
	color := deltaColor(v, false)

	sign := ""
	if v > 0 {
		sign = "+"
	}

	s := fmt.Sprintf("%s%d", sign, v)
	if pct != nil {
		s += fmt.Sprintf(" (%.1f%%)", *pct)
	}

	return template.HTML(fmt.Sprintf(`<span style="color:%s">%s</span>`, color, s)) //nolint:gosec // computed values only, no user input
}

func deltaColor(v int64, invert bool) string {
	switch {
	case v > 0 && !invert, v < 0 && invert:
		return deltaColorGood
	case v < 0 && !invert, v > 0 && invert:
		return deltaColorBad
	default:
		return deltaColorNone
	}
}

func init() {
	// Simple pages using layout.html only.
	simplePages := []string{
		"settings.html", "changelog.html", "suspended.html",
		"admin.html", "admin_tenants.html", "admin_tenant.html",
		"admin_tokens.html", "admin_metrics.html",
	}
	// Pages that also need the plans_table partial.
	planPages := []string{"landing.html", "tos.html", "help.html"}

	pageTemplates = make(map[string]*template.Template, len(simplePages)+len(planPages)+1)
	for _, p := range simplePages {
		pageTemplates[p] = template.Must(template.New("").Funcs(templateFuncs).ParseFS(templateFS,
			"templates/layout.html", "templates/"+p))
	}
	for _, p := range planPages {
		pageTemplates[p] = template.Must(template.New("").Funcs(templateFuncs).ParseFS(templateFS,
			"templates/layout.html", "templates/plans_table.html", "templates/"+p))
	}
	pageTemplates["home.html"] = template.Must(template.New("").Funcs(templateFuncs).ParseFS(templateFS,
		"templates/layout.html", "templates/home.html"))
}

const (
	sessionTTL              = 7 * 24 * time.Hour
	serverReadTimeout       = 30 * time.Second
	serverReadHeaderTimeout = 5 * time.Second
	serverWriteTimeout      = 60 * time.Second
	serverIdleTimeout       = 120 * time.Second
	serverMaxHeaderBytes    = 64 * 1024 // 64KB
	addressDefault          = "0.0.0.0"
	portDefault             = "8080"
)

// Options configures the server.
type Options struct {
	Version string
	Commit  string
	Date    string
}

// Run starts the HTTP server. It blocks until the context is canceled.
func Run(ctx context.Context, opts Options) error {
	serverOpts = opts

	store, err := postgres.NewFromEnv()
	if err != nil {
		return fmt.Errorf("opening store: %w", err)
	}
	defer func() {
		if closeErr := store.Close(); closeErr != nil {
			slog.Error("closing store", "error", closeErr)
		}
	}()

	db := store.DB()
	port := config.GetEnv("PORT", portDefault)
	baseURL := strings.TrimRight(os.Getenv("BASE_URL"), "/")

	oauthCfg := &oauth.Config{
		ClientID:     os.Getenv("GITHUB_OAUTH_CLIENT_ID"),
		ClientSecret: os.Getenv("GITHUB_OAUTH_CLIENT_SECRET"),
		RedirectURL:  baseURL + "/auth/github/callback",
	}
	webhookSecret := os.Getenv("GITHUB_WEBHOOK_SECRET")
	ghAppID, _ := strconv.ParseInt(os.Getenv("GITHUB_APP_ID"), 10, 64)

	trigger, err := newImportTrigger(ctx, config.ImportJobName())
	if err != nil {
		slog.Warn("import trigger unavailable", "error", err)
	}
	defer func() {
		if closeErr := trigger.Close(); closeErr != nil {
			slog.Error("closing import trigger", "error", closeErr)
		}
	}()

	apiCache.startEviction(ctx)

	oauthRL := newRateLimiter(config.OAuthRateLimit(), time.Minute)
	defer oauthRL.stop()
	repoSearchRL := newRateLimiter(config.RepoSearchRateLimit(), time.Minute)
	defer repoSearchRL.stop()
	unsubRL := newRateLimiter(5, time.Minute)
	defer unsubRL.stop()

	mux := makeRouter(db, store, oauthCfg, webhookSecret, opts, oauthRL, repoSearchRL, unsubRL, trigger, ghAppID)

	address := fmt.Sprintf("%s:%s", addressDefault, port)
	s := &http.Server{
		Addr:              address,
		Handler:           middleware.Recovery(securityHeaders(mux)),
		ReadTimeout:       serverReadTimeout,
		ReadHeaderTimeout: serverReadHeaderTimeout,
		WriteTimeout:      serverWriteTimeout,
		IdleTimeout:       serverIdleTimeout,
		MaxHeaderBytes:    serverMaxHeaderBytes,
	}

	errCh := make(chan error, 1)
	go func() {
		if err := s.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	slog.Info("server started", "address", address)

	select {
	case err := <-errCh:
		return fmt.Errorf("server failed: %w", err)
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Duration(config.ServerShutdownTimeout())*time.Second)
	defer cancel()

	if err := s.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("shutdown failed", "error", err)
	}

	return nil
}

func makeRouter(
	db *sql.DB, store data.Store, oauthCfg *oauth.Config, webhookSecret string,
	opts Options, oauthRLimiter, repoSearchRLimiter, unsubRLimiter *rateLimiter,
	trigger *importTrigger, ghAppID int64,
) *http.ServeMux {
	mux := http.NewServeMux()

	// Static assets
	mux.Handle("GET /static/", http.FileServer(http.FS(staticFS)))

	// Rate limit middleware for abuse-sensitive endpoints.
	oauthRL := rateLimitMiddleware(oauthRLimiter)
	repoSearchRL := rateLimitMiddleware(repoSearchRLimiter)
	unsubRL := rateLimitMiddleware(unsubRLimiter)

	// Public routes (no auth)
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("GET /{$}", landingHandler(opts))
	mux.Handle("GET /auth/github", oauthRL(oauthStartHandler(oauthCfg)))
	mux.HandleFunc("GET /auth/github/callback", oauthCallbackHandler(db, oauthCfg))
	mux.HandleFunc("POST /webhook/github", WebhookHandler(db, webhookSecret))
	mux.HandleFunc("GET /help", helpPageHandler(db))
	mux.HandleFunc("POST /help/contact", helpContactHandler(db))
	mux.HandleFunc("GET /changelog", func(w http.ResponseWriter, _ *http.Request) {
		renderTemplate(w, "changelog.html", pageData{Title: "Changelog"})
	})
	mux.HandleFunc("GET /suspended", func(w http.ResponseWriter, _ *http.Request) {
		renderTemplate(w, "suspended.html", pageData{Title: "Account Suspended"})
	})
	mux.Handle("GET /digest/unsubscribe", unsubRL(digestUnsubscribeHandler(db)))
	mux.HandleFunc("GET /auth/reset", func(w http.ResponseWriter, r *http.Request) {
		middleware.ClearSessionCookie(w)
		http.Redirect(w, r, "/", http.StatusFound)
	})

	// Auth middleware
	auth := middleware.RequireAuth(db, "/auth/github")
	wrap := func(h http.HandlerFunc) http.Handler {
		return auth(h)
	}

	// Authenticated routes
	mux.Handle("GET /tos", wrap(tosPageHandler()))
	mux.Handle("POST /tos/accept", wrap(tosAcceptHandler(db)))
	mux.Handle("GET /dashboard", wrap(dashboardHandler(opts)))
	mux.Handle("GET /settings", wrap(settingsHandler(db)))
	mux.Handle("POST /settings/digest", wrap(digestToggleHandler(db)))
	mux.Handle("POST /auth/signout", wrap(signoutHandler(db)))

	// Tenant management API
	mux.Handle("GET /api/repos", wrap(listReposHandler(db)))
	mux.Handle("GET /api/repos/overview", wrap(repoOverviewHandler(db)))
	mux.Handle("POST /api/repos", wrap(addRepoHandler(db, trigger, ghAppID)))
	mux.Handle("DELETE /api/repos/{org}/{repo}", wrap(deleteRepoHandler(db)))
	mux.Handle("POST /api/upgrade-request", wrap(upgradeRequestHandler(db)))
	mux.Handle("GET /api/repos/available", repoSearchRL(wrap(availableReposHandler(db))))
	mux.Handle("GET /api/installations", wrap(listInstallationsHandler(db)))

	// Data API (chart endpoints, authenticated + tenant-scoped via RLS)
	scopedWrap := func(h http.HandlerFunc) http.Handler {
		return auth(ScopedStoreMiddleware(db)(h))
	}
	mux.Handle("GET /data/min-date", scopedWrap(minDateAPIHandler(store)))
	mux.Handle("GET /data/query", scopedWrap(queryAPIHandler(store)))
	mux.Handle("GET /data/type", scopedWrap(eventDataAPIHandler(store)))
	mux.Handle("GET /data/entity", scopedWrap(entityDataAPIHandler(store)))
	mux.Handle("GET /data/developer", scopedWrap(developerDataAPIHandler(store)))
	mux.Handle("POST /data/search", scopedWrap(eventSearchAPIHandler(store)))
	mux.Handle("GET /data/entity/developers", scopedWrap(entityDevelopersAPIHandler(store)))
	mux.Handle("GET /data/developer/search", scopedWrap(developerSearchAPIHandler(store)))
	mux.Handle("GET /data/insights/summary", scopedWrap(insightsSummaryAPIHandler(store)))
	mux.Handle("GET /data/insights/daily-activity", scopedWrap(insightsDailyActivityAPIHandler(store)))
	mux.Handle("GET /data/insights/retention", scopedWrap(insightsRetentionAPIHandler(store)))
	mux.Handle("GET /data/insights/pr-ratio", scopedWrap(insightsPRRatioAPIHandler(store)))
	mux.Handle("GET /data/insights/time-to-merge", scopedWrap(insightsTimeToMergeAPIHandler(store)))
	mux.Handle("GET /data/insights/time-to-close", scopedWrap(insightsTimeToCloseAPIHandler(store)))
	mux.Handle("GET /data/insights/time-to-restore", scopedWrap(insightsTimeToRestoreAPIHandler(store)))
	mux.Handle("GET /data/insights/review-latency", scopedWrap(insightsReviewLatencyAPIHandler(store)))
	mux.Handle("GET /data/insights/forks-and-activity", scopedWrap(insightsForksAndActivityAPIHandler(store)))
	mux.Handle("GET /data/insights/repo-meta", scopedWrap(insightsRepoMetaAPIHandler(store)))
	mux.Handle("GET /data/insights/repo-overview", scopedWrap(insightsRepoOverviewAPIHandler(store)))
	mux.Handle("GET /data/insights/repo-metric-history", scopedWrap(insightsRepoMetricHistoryAPIHandler(store)))
	mux.Handle("GET /data/insights/change-failure-rate", scopedWrap(insightsChangeFailureRateAPIHandler(store)))
	mux.Handle("GET /data/insights/pr-size", scopedWrap(insightsPRSizeAPIHandler(store)))
	mux.Handle("GET /data/insights/contributor-momentum", scopedWrap(insightsContributorMomentumAPIHandler(store)))
	mux.Handle("GET /data/insights/contributor-funnel", scopedWrap(insightsContributorFunnelAPIHandler(store)))
	mux.Handle("GET /data/insights/contributor-profile", scopedWrap(insightsContributorProfileAPIHandler(store)))
	mux.Handle("GET /data/insights/release-cadence", scopedWrap(insightsReleaseCadenceAPIHandler(store)))
	mux.Handle("GET /data/insights/release-downloads", scopedWrap(insightsReleaseDownloadsAPIHandler(store)))
	mux.Handle("GET /data/insights/release-downloads-by-tag", scopedWrap(insightsReleaseDownloadsByTagAPIHandler(store)))
	mux.Handle("GET /data/insights/container-activity", scopedWrap(insightsContainerActivityAPIHandler(store)))
	mux.Handle("GET /data/insights/contributor-composition", scopedWrap(insightsContributorCompositionAPIHandler(store)))
	mux.Handle("GET /data/insights/issue-ratio", scopedWrap(insightsIssueRatioAPIHandler(store)))
	mux.Handle("GET /data/insights/time-to-first-response", scopedWrap(insightsTimeToFirstResponseAPIHandler(store)))
	mux.Handle("GET /data/insights/health-scorecard", scopedWrap(insightsHealthScorecardHandler(store)))
	mux.Handle("GET /data/insights/portfolio-summary", scopedWrap(insightsPortfolioSummaryHandler(store)))
	mux.Handle("GET /data/insights/signals", scopedWrap(insightsSignalsHandler(store)))
	mux.Handle("GET /data/insights/generated", scopedWrap(insightsGeneratedAPIHandler(store)))

	// Batch endpoints: one request per tab, reduces connection overhead.
	mux.Handle("GET /data/batch/health", scopedWrap(batchHealthHandler(store)))
	mux.Handle("GET /data/batch/activity", scopedWrap(batchActivityHandler(store)))
	mux.Handle("GET /data/batch/velocity", scopedWrap(batchVelocityHandler(store)))
	mux.Handle("GET /data/batch/quality", scopedWrap(batchQualityHandler(store)))
	mux.Handle("GET /data/batch/community", scopedWrap(batchCommunityHandler(store)))

	mux.Handle("GET /data/export/csv", scopedWrap(csvExportHandler(store, func(ctx context.Context, tenantID string) ([]tenant.TenantRepo, error) {
		return tenant.ListTenantRepos(ctx, db, tenantID)
	})))

	registerAdminRoutes(mux, db)

	return mux
}

// registerAdminRoutes adds admin pages (session auth + username whitelist, full SSR).
func registerAdminRoutes(mux *http.ServeMux, db *sql.DB) {
	requireAdmin := middleware.RequireAdmin(db)
	wrap := func(h http.HandlerFunc) http.Handler {
		return requireAdmin(h)
	}
	mcfg := newMetricsConfig()
	mux.Handle("GET /admin", wrap(adminDashboardHandler(db)))
	mux.Handle("GET /admin/tenants", wrap(adminTenantsHandler(db)))
	mux.Handle("GET /admin/tenant/{username}", wrap(adminTenantDetailHandler(db)))
	mux.Handle("POST /admin/tenant/{username}/plan", wrap(adminUpdatePlanHandler(db)))
	mux.Handle("POST /admin/tenant/{username}/status", wrap(adminUpdateStatusHandler(db)))
	mux.Handle("POST /admin/invite", wrap(adminInviteHandler(db)))
	mux.Handle("POST /admin/tenant/{username}/reset", wrap(adminResetErrorsHandler(db)))
	mux.Handle("POST /admin/tenant/{username}/hard-reset", wrap(adminHardResetHandler(db)))
	mux.Handle("GET /admin/tokens", wrap(adminTokensHandler(db)))
	mux.Handle("GET /admin/tokens/quota-history", wrap(adminTokenQuotaHistoryHandler(db)))
	mux.Handle("GET /admin/metrics", wrap(adminMetricsHandler(mcfg)))
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
		w.Header().Set("Content-Security-Policy",
			"default-src 'self';"+
				" script-src 'self' https://ajax.googleapis.com https://cdn.jsdelivr.net;"+
				" style-src 'self' 'unsafe-inline';"+
				" font-src 'self';"+
				" img-src 'self' https://avatars.githubusercontent.com data:;"+
				" connect-src 'self'; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}

func renderTemplate(w http.ResponseWriter, name string, data any) {
	t, ok := pageTemplates[name]
	if !ok {
		slog.Error("template not found", "name", name)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, "layout.html", data); err != nil {
		slog.Error("failed to render template", "name", name, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = buf.WriteTo(w)
}

type pageData struct {
	Title        string
	Username     string
	GitHubAppURL string
	Version      string
	Commit       string
	Date         string
	PlanMap      map[string]plan.Plan
	Plans        []plan.Plan
	Features     []plan.Feature
	Error        string
}

// errMessages maps query param error codes to user-friendly messages.
var errMessages = map[string]string{
	"auth_failed":  "Authentication failed. Please try again.",
	"auth_expired": "Your sign-in session expired. Please try again.",
	"rate_limit":   "Too many requests. Please wait a moment and try again.",
}

func landingHandler(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var errMsg string
		if code := r.URL.Query().Get("err"); code != "" {
			if msg, ok := errMessages[code]; ok {
				errMsg = msg
			}
		}
		renderTemplate(w, "landing.html", pageData{
			Title:    "Home",
			Version:  opts.Version,
			Commit:   opts.Commit,
			Date:     opts.Date,
			PlanMap:  plan.All,
			Plans:    plan.DisplayPlans(),
			Features: plan.DisplayFeatures(),
			Error:    errMsg,
		})
	}
}

func dashboardHandler(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tn := middleware.TenantFromContext(r.Context())
		if tn == nil {
			http.Redirect(w, r, "/auth/github", http.StatusFound)
			return
		}
		limits, _ := plan.Get(tn.Plan)

		renderTemplate(w, "home.html", map[string]any{
			"Title":         "Dashboard",
			"base_path":     "",
			"version":       opts.Version,
			"commit":        opts.Commit,
			"build_date":    opts.Date,
			"period_days":   180,
			"username":      tn.Username,
			"name":          tn.Name,
			"avatar_url":    tn.AvatarURL,
			"plan":          tn.Plan,
			"pdf_export":    limits.PDFExport,
			"csv_export":    limits.CSVExport,
			"max_data_days": limits.MaxDataRangeMonths * 30,
			"ai_level":      limits.AILevel,
		})
	}
}

func settingsHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tn := middleware.TenantFromContext(r.Context())
		if tn == nil {
			http.Redirect(w, r, "/auth/github", http.StatusFound)
			return
		}

		limits, _ := plan.Get(tn.Plan)
		repoCount, _ := tenant.CountTenantRepos(r.Context(), db, tn.ID)
		lastSignIn := tenant.GetLastSignIn(r.Context(), db, tn.ID)

		const unlimitedLabel = "unlimited"
		maxRepos := fmt.Sprintf("%d", limits.MaxRepos)
		if limits.MaxRepos == 0 {
			maxRepos = unlimitedLabel
		}
		maxEvents := fmt.Sprintf("%d", limits.MaxEventsPerWeek)
		if limits.MaxEventsPerWeek == 0 {
			maxEvents = unlimitedLabel
		}
		lastLogin := "never"
		if lastSignIn != nil {
			lastLogin = lastSignIn.Format("2006-01-02")
		}

		renderTemplate(w, "settings.html", map[string]any{
			"Title":         "Settings",
			"username":      tn.Username,
			"name":          tn.Name,
			"email":         tn.Email,
			"company":       tn.Company,
			"location":      tn.Location,
			"bio":           tn.Bio,
			"plan":          tn.Plan,
			"repo_count":    repoCount,
			"max_repos":     maxRepos,
			"max_events":    maxEvents,
			"weekly_digest": tn.WeeklyDigest,
			"created_at":    tn.CreatedAt.Format("2006-01-02"),
			"last_login":    lastLogin,
		})
	}
}

func digestToggleHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tn := middleware.TenantFromContext(r.Context())
		if tn == nil {
			http.Redirect(w, r, "/auth/github", http.StatusFound)
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, 1024)
		enabled := r.FormValue("enabled") == "true"
		if err := tenant.UpdateWeeklyDigest(r.Context(), db, tn.ID, enabled); err != nil {
			slog.Error("updating weekly digest", "tenant", tn.Username, "error", err)
			http.Error(w, "failed to update preference", http.StatusInternalServerError)
			return
		}

		http.Redirect(w, r, "/settings", http.StatusSeeOther)
	}
}

func digestUnsubscribeHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tenantID := r.URL.Query().Get("tenant")
		token := r.URL.Query().Get("token")
		secret := digest.HMACSecret()

		if tenantID == "" || token == "" || secret == "" {
			http.Error(w, "invalid unsubscribe link", http.StatusBadRequest)
			return
		}

		if !digest.ValidateUnsubscribeToken(secret, tenantID, token) {
			http.Error(w, "invalid unsubscribe link", http.StatusForbidden)
			return
		}

		if err := tenant.UpdateWeeklyDigest(r.Context(), db, tenantID, false); err != nil {
			slog.Error("unsubscribing from digest", "tenant", tenantID, "error", err)
			http.Error(w, "failed to unsubscribe", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<!DOCTYPE html><html><head><meta charset="utf-8"><title>Unsubscribed</title></head>
<body style="background:#111;color:#ccc;font-family:sans-serif;display:flex;align-items:center;justify-content:center;min-height:100vh;margin:0;">
<div style="text-align:center;max-width:400px;">
<h1 style="color:#fff;font-size:20px;">Unsubscribed</h1>
<p>You've been unsubscribed from the weekly DevPulse digest.</p>
<p style="color:#888;font-size:13px;">You can re-enable it anytime from your <a href="/settings" style="color:#6366f1;">Settings</a> page.</p>
</div></body></html>`)
	}
}

func oauthStartHandler(cfg *oauth.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		url, state, err := oauth.BuildAuthURL(cfg)
		if err != nil {
			slog.Error("building oauth URL", "error", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		http.SetCookie(w, &http.Cookie{
			Name:     middleware.OAuthStateCookieName(),
			Value:    state,
			Path:     "/",
			MaxAge:   600,
			Secure:   middleware.IsSecure(),
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
		})
		http.Redirect(w, r, url, http.StatusFound)
	}
}

func oauthCallbackHandler(db *sql.DB, cfg *oauth.Config) http.HandlerFunc {
	clearAndRedirect := func(w http.ResponseWriter, r *http.Request, msg string) {
		// Clear all auth cookies so the user can retry cleanly.
		http.SetCookie(w, &http.Cookie{
			Name: middleware.OAuthStateCookieName(), Value: "", MaxAge: -1, Path: "/",
			HttpOnly: true, Secure: middleware.IsSecure(), SameSite: http.SameSiteLaxMode,
		})
		middleware.ClearSessionCookie(w)
		http.Redirect(w, r, "/?err="+url.QueryEscape(msg), http.StatusSeeOther)
	}

	return func(w http.ResponseWriter, r *http.Request) {
		stateCookie, err := r.Cookie(middleware.OAuthStateCookieName())
		if err != nil || subtle.ConstantTimeCompare(
			[]byte(stateCookie.Value),
			[]byte(r.URL.Query().Get("state")),
		) != 1 {
			slog.Warn("oauth state mismatch", "has_cookie", err == nil)
			clearAndRedirect(w, r, "auth_expired")
			return
		}

		http.SetCookie(w, &http.Cookie{
			Name:     middleware.OAuthStateCookieName(),
			Value:    "",
			MaxAge:   -1,
			Path:     "/",
			HttpOnly: true,
			Secure:   middleware.IsSecure(),
			SameSite: http.SameSiteLaxMode,
		})

		code := r.URL.Query().Get("code")
		token, err := oauth.ExchangeCode(r.Context(), cfg, code)
		if err != nil {
			// Code exchange can fail if the code was already consumed (e.g.,
			// browser prefetch, double-click, or redirect replay). If the user
			// already has a valid session from the first successful callback,
			// send them to the dashboard instead of showing an error.
			if sessionCookie, cookieErr := r.Cookie(middleware.SessionCookieName()); cookieErr == nil {
				if _, valErr := tenant.ValidateSession(r.Context(), db, sessionCookie.Value); valErr == nil {
					http.Redirect(w, r, "/dashboard", http.StatusFound)
					return
				}
			}
			slog.Error("oauth exchange failed", "error", err)
			clearAndRedirect(w, r, "auth_failed")
			return
		}

		user, err := oauth.FetchUser(r.Context(), cfg, token)
		if err != nil {
			slog.Error("fetching github user", "error", err)
			clearAndRedirect(w, r, "auth_failed")
			return
		}

		tn, sessionToken, err := tenant.AuthenticateUser(r.Context(), db, user.ID, user.Login, user.Email, user.AvatarURL, user.Name, user.Company, user.Location, user.Bio, sessionTTL)
		if err != nil {
			slog.Error("authenticating user", "error", err)
			clearAndRedirect(w, r, "auth_failed")
			return
		}

		if tn.Status != tenant.StatusActive {
			slog.Warn("suspended user login attempt", "username", tn.Username)
			http.Redirect(w, r, "/suspended", http.StatusFound)
			return
		}

		slog.Info("user signed in", "username", tn.Username, "tenant_id", tn.ID)

		middleware.SetSessionCookie(w, sessionToken, int(sessionTTL.Seconds()))

		if tn.ToSAcceptedAt == nil {
			http.Redirect(w, r, "/tos", http.StatusFound)
			return
		}

		http.Redirect(w, r, "/dashboard", http.StatusFound)
	}
}

func signoutHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if cookie, err := r.Cookie(middleware.SessionCookieName()); err == nil {
			if derr := tenant.DestroySession(r.Context(), db, cookie.Value); derr != nil {
				slog.Debug("destroying session", "error", derr)
			}
		}
		middleware.ClearSessionCookie(w)
		http.Redirect(w, r, "/", http.StatusFound)
	}
}

func tosPageHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		renderTemplate(w, "tos.html", pageData{Title: "Terms of Service", PlanMap: plan.All})
	}
}

func tosAcceptHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tn := middleware.TenantFromContext(r.Context())
		if tn == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if err := tenant.AcceptToS(r.Context(), db, tn.ID); err != nil {
			slog.Error("accepting tos", "error", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		slog.Info("tos accepted", "tenant_id", tn.ID, "username", tn.Username)
		http.Redirect(w, r, "/dashboard", http.StatusFound)
	}
}

func tenantListHandler(db *sql.DB, label string, queryFn func(context.Context, *sql.DB, string) (any, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tn := middleware.TenantFromContext(r.Context())
		if tn == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		result, err := queryFn(r.Context(), db, tn.ID)
		if err != nil {
			slog.Error("listing failed", "resource", label, "error", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func listReposHandler(db *sql.DB) http.HandlerFunc {
	return tenantListHandler(db, "repos", func(ctx context.Context, d *sql.DB, id string) (any, error) {
		return tenant.ListTenantRepos(ctx, d, id)
	})
}

func listInstallationsHandler(db *sql.DB) http.HandlerFunc {
	return tenantListHandler(db, "installations", func(ctx context.Context, d *sql.DB, id string) (any, error) {
		return tenant.ListInstallations(ctx, d, id)
	})
}

func addRepoHandler(db *sql.DB, trigger *importTrigger, ghAppID int64) http.HandlerFunc {
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

		// Repo must be publicly accessible.
		if !isPublicRepo(r.Context(), org, repo) {
			http.Error(w, "repo not found or not public", http.StatusForbidden)
			return
		}

		// Tenant must have at least one GitHub App installation.
		// Try org-specific first, then fall back to any active installation
		// (any installation token can access public repos).
		install, err := tenant.GetInstallationForOrg(r.Context(), db, tn.ID, org, ghAppID)
		if err != nil {
			slog.Error("checking installation", "org", org, "error", err)
			http.Error(w, "error checking installation", http.StatusInternalServerError)
			return
		}
		if install == nil {
			installs, err := tenant.GetActiveInstallations(r.Context(), db, tn.ID, ghAppID)
			if err != nil {
				slog.Error("checking installations", "error", err)
				http.Error(w, "error checking installation", http.StatusInternalServerError)
				return
			}
			if len(installs) > 0 {
				install = &installs[0]
			}
		}
		if install == nil {
			http.Error(w, "no GitHub App installation found — go to https://github.com/apps/DevPulseThingz and click Configure to install the app",
				http.StatusBadRequest)
			return
		}

		if err := tenant.AddTenantRepos(r.Context(), db, tn.ID, []tenant.OrgRepo{{Org: org, Repo: repo}}); err != nil {
			slog.Error("adding repo", "error", err)
			if errors.Is(err, tenant.ErrRepoLimitExceeded) {
				http.Error(w, fmt.Sprintf("repo_limit_reached:%d", tn.MaxRepos), http.StatusForbidden)
				return
			}
			http.Error(w, "error adding repository", http.StatusInternalServerError)
			return
		}

		go func() { //nolint:gosec // fire-and-forget: goroutine intentionally outlives the HTTP request
			triggerCtx, cancel := context.WithTimeout(context.Background(), time.Duration(config.ServerTriggerTimeout())*time.Second)
			defer cancel()
			if triggerErr := trigger.TriggerRepoImport(triggerCtx, org, repo); triggerErr != nil {
				slog.Error("triggering on-demand import", "org", org, "repo", repo, "error", triggerErr)
			}
		}()

		w.WriteHeader(http.StatusCreated)
	}
}

func upgradeRequestHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tn := middleware.TenantFromContext(r.Context())
		if tn == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		req, err := tenant.RequestUpgrade(r.Context(), db, tn.ID)
		if err != nil {
			slog.Error("upgrade request failed", "tenant_id", tn.ID, "error", err)
			http.Error(w, "error processing request", http.StatusInternalServerError)
			return
		}

		if req != nil {
			slog.Info("upgrade requested",
				"tenant_id", tn.ID,
				"username", req.Username,
				"email", req.Email,
				"plan", req.Plan,
				"max_repos", req.MaxRepos,
			)
		}

		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}

// isPublicRepo checks if a GitHub repo is publicly accessible.
func isPublicRepo(ctx context.Context, org, repo string) bool {
	ghURL := fmt.Sprintf("https://api.github.com/repos/%s/%s",
		url.PathEscape(org), url.PathEscape(repo))
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, ghURL, nil) //nolint:gosec // constant base URL
	if err != nil {
		return false
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := net.GitHubClient.Do(req) //nolint:gosec // constant base URL
	if err != nil {
		return false
	}
	resp.Body.Close()

	return resp.StatusCode == http.StatusOK
}

func repoOverviewHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := dataCacheKey(r)
		if cached, ok := apiCache.get(key); ok {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set(cacheControlHeaderKey, browserCacheMaxAge)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(cached) //nolint:gosec // cached bytes are from our own json.Marshal
			return
		}

		tn := middleware.TenantFromContext(r.Context())
		if tn == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		days := queryParamInt(r, "d", 180)
		overview, err := tenant.GetOverview(r.Context(), db, tn.ID, days)
		if err != nil {
			slog.Error("getting repo overview", "error", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		b, marshalErr := json.Marshal(overview)
		if marshalErr != nil {
			writeJSON(w, http.StatusOK, overview)
			return
		}
		apiCache.setWithTTL(key, b, requestCacheTTL(r))
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set(cacheControlHeaderKey, browserCacheMaxAge)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(b)
	}
}

func deleteRepoHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tn := middleware.TenantFromContext(r.Context())
		if tn == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		org := r.PathValue("org")
		repo := r.PathValue("repo")
		if org == "" || repo == "" {
			http.Error(w, "org and repo required", http.StatusBadRequest)
			return
		}

		if err := tenant.DeactivateTenantRepo(r.Context(), db, tn.ID, org, repo); err != nil {
			slog.Error("deactivating repo", "error", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusNoContent)
	}
}

func availableReposHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tn := middleware.TenantFromContext(r.Context())
		if tn == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		query := strings.TrimSpace(r.URL.Query().Get("q"))
		if len(query) < 2 {
			writeJSON(w, http.StatusOK, []tenant.OrgRepo{})
			return
		}
		if len(query) > 128 {
			writeJSON(w, http.StatusOK, []tenant.OrgRepo{})
			return
		}

		// Search GitHub public repos.
		// If query contains org/repo pattern, use org: qualifier for better matching.
		searchQ := query
		if parts := strings.SplitN(query, "/", 2); len(parts) == 2 && parts[0] != "" && parts[1] != "" {
			searchQ = parts[1] + " in:name org:" + parts[0]
		}
		ghURL := fmt.Sprintf("https://api.github.com/search/repositories?q=%s&per_page=10",
			url.QueryEscape(searchQ))
		req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, ghURL, nil) //nolint:gosec // constant base URL, query param is url-escaped
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		req.Header.Set("Accept", "application/vnd.github+json")

		resp, err := net.GitHubClient.Do(req) //nolint:gosec // URL constructed from constant base + user query param
		if err != nil {
			slog.Error("searching github repos", "error", err)
			writeJSON(w, http.StatusOK, []tenant.OrgRepo{})
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			writeJSON(w, http.StatusOK, []tenant.OrgRepo{})
			return
		}

		var result struct {
			Items []struct {
				FullName string `json:"full_name"`
			} `json:"items"`
		}
		if decErr := json.NewDecoder(resp.Body).Decode(&result); decErr != nil {
			writeJSON(w, http.StatusOK, []tenant.OrgRepo{})
			return
		}

		// Filter out already-tracked repos
		tracked, err := tenant.ListTenantRepos(r.Context(), db, tn.ID)
		if err != nil {
			slog.Warn("listing tracked repos for dedup filter", "error", err)
			// continue with empty set — search still works, dedup filtering is best-effort
		}
		trackedSet := make(map[string]bool, len(tracked))
		for _, tr := range tracked {
			trackedSet[tr.Org+"/"+tr.Repo] = true
		}

		var available []tenant.OrgRepo
		for _, item := range result.Items {
			parts := strings.SplitN(item.FullName, "/", 2)
			if len(parts) == 2 && !trackedSet[item.FullName] {
				available = append(available, tenant.OrgRepo{Org: parts[0], Repo: parts[1]})
			}
		}

		writeJSON(w, http.StatusOK, available)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		slog.Error("failed to marshal JSON", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(b)
}
