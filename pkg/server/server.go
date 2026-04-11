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
	"strings"
	"time"

	"github.com/thingzio/devpulse/pkg/config"
	"github.com/thingzio/devpulse/pkg/data"
	"github.com/thingzio/devpulse/pkg/data/postgres"
	"github.com/thingzio/devpulse/pkg/middleware"
	"github.com/thingzio/devpulse/pkg/oauth"
	"github.com/thingzio/devpulse/pkg/plan"
	"github.com/thingzio/devpulse/pkg/tenant"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static/*
var staticFS embed.FS

var pageTemplates map[string]*template.Template

var templateFuncs = template.FuncMap{
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
}

func init() {
	// Simple pages using layout.html
	simplePages := []string{"landing.html", "tos.html", "help.html", "settings.html"}
	pageTemplates = make(map[string]*template.Template, len(simplePages)+1)
	for _, p := range simplePages {
		pageTemplates[p] = template.Must(template.New("").Funcs(templateFuncs).ParseFS(templateFS,
			"templates/layout.html", "templates/"+p))
	}
	// Dashboard uses the old header/home/footer pattern
	pageTemplates["home.html"] = template.Must(template.New("").Funcs(templateFuncs).ParseFS(templateFS,
		"templates/header.html", "templates/home.html", "templates/footer.html"))
}

const (
	sessionTTL              = 7 * 24 * time.Hour
	serverReadTimeout       = 30 * time.Second
	serverReadHeaderTimeout = 5 * time.Second
	serverWriteTimeout      = 60 * time.Second
	serverIdleTimeout       = 120 * time.Second
	serverMaxHeaderBytes    = 64 * 1024 // 64KB
	externalHTTPTimeout     = 10 * time.Second

	addressDefault = "0.0.0.0"
	portDefault    = "8080"
)

// httpClient is used for all outbound HTTP calls (GitHub API, etc.)
var httpClient = &http.Client{Timeout: externalHTTPTimeout}

// Options configures the server.
type Options struct {
	Version string
	Commit  string
	Date    string
}

// Run starts the HTTP server. It blocks until the context is canceled.
func Run(ctx context.Context, opts Options) error {
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

	oauthRL := newRateLimiter(20, time.Minute)
	repoSearchRL := newRateLimiter(30, time.Minute)

	mux := makeRouter(db, store, oauthCfg, webhookSecret, opts, oauthRL, repoSearchRL, trigger)

	address := fmt.Sprintf("%s:%s", addressDefault, port)
	s := &http.Server{
		Addr:              address,
		Handler:           securityHeaders(mux),
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

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := s.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("shutdown failed", "error", err)
	}

	oauthRL.stop()
	repoSearchRL.stop()

	return nil
}

func makeRouter(db *sql.DB, store data.Store, oauthCfg *oauth.Config, webhookSecret string, opts Options, oauthRLimiter, repoSearchRLimiter *rateLimiter, trigger *importTrigger) *http.ServeMux {
	mux := http.NewServeMux()

	// Static assets
	mux.Handle("GET /static/", http.FileServer(http.FS(staticFS)))

	// Rate limit middleware for abuse-sensitive endpoints.
	oauthRL := rateLimitMiddleware(oauthRLimiter)
	repoSearchRL := rateLimitMiddleware(repoSearchRLimiter)

	// Public routes (no auth)
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("GET /{$}", landingHandler(opts))
	mux.Handle("GET /auth/github", oauthRL(oauthStartHandler(oauthCfg)))
	mux.HandleFunc("GET /auth/github/callback", oauthCallbackHandler(db, oauthCfg))
	mux.HandleFunc("POST /webhook/github", WebhookHandler(db, webhookSecret))
	mux.HandleFunc("GET /help", func(w http.ResponseWriter, _ *http.Request) {
		renderTemplate(w, "help.html", pageData{Title: "Help", Plans: plan.All})
	})
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
	mux.Handle("POST /auth/signout", wrap(signoutHandler(db)))

	// Tenant management API
	mux.Handle("GET /api/repos", wrap(listReposHandler(db)))
	mux.Handle("GET /api/repos/overview", wrap(repoOverviewHandler(db)))
	mux.Handle("POST /api/repos", wrap(addRepoHandler(db, trigger)))
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
	mux.Handle("GET /data/insights/reputation", scopedWrap(insightsReputationAPIHandler(store)))
	mux.Handle("GET /data/insights/issue-ratio", scopedWrap(insightsIssueRatioAPIHandler(store)))
	mux.Handle("GET /data/insights/time-to-first-response", scopedWrap(insightsTimeToFirstResponseAPIHandler(store)))
	mux.Handle("GET /data/insights/health-scorecard", scopedWrap(insightsHealthScorecardHandler(store)))
	mux.Handle("GET /data/insights/portfolio-summary", scopedWrap(insightsPortfolioSummaryHandler(store)))
	mux.Handle("GET /data/insights/signals", scopedWrap(insightsSignalsHandler(store)))
	mux.Handle("GET /data/insights/generated", scopedWrap(insightsGeneratedAPIHandler(store)))
	mux.Handle("GET /data/export/csv", scopedWrap(csvExportHandler(store, func(ctx context.Context, tenantID string) ([]tenant.TenantRepo, error) {
		return tenant.ListTenantRepos(ctx, db, tenantID)
	})))

	return mux
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
				" style-src 'self' 'unsafe-inline' https://fonts.googleapis.com;"+
				" font-src 'self' https://fonts.gstatic.com;"+
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
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
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
	Plans        map[string]plan.Limits
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
			Title:   "Home",
			Version: opts.Version,
			Commit:  opts.Commit,
			Date:    opts.Date,
			Plans:   plan.All,
			Error:   errMsg,
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

		t := pageTemplates["home.html"]
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := t.ExecuteTemplate(w, "home", map[string]any{
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
		}); err != nil {
			slog.Error("rendering dashboard", "error", err)
		}
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
			"Title":      "Settings",
			"username":   tn.Username,
			"name":       tn.Name,
			"email":      tn.Email,
			"plan":       tn.Plan,
			"repo_count": repoCount,
			"max_repos":  maxRepos,
			"max_events": maxEvents,
			"created_at": tn.CreatedAt.Format("2006-01-02"),
			"last_login": lastLogin,
		})
	}
}

func oauthStartHandler(cfg *oauth.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		url, state := oauth.BuildAuthURL(cfg)
		http.SetCookie(w, &http.Cookie{
			Name:     "oauth_state",
			Value:    state,
			Path:     "/",
			MaxAge:   600,
			Secure:   true,
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
			Name: "oauth_state", Value: "", MaxAge: -1, Path: "/",
			HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode,
		})
		middleware.ClearSessionCookie(w)
		http.Redirect(w, r, "/?err="+url.QueryEscape(msg), http.StatusSeeOther)
	}

	return func(w http.ResponseWriter, r *http.Request) {
		stateCookie, err := r.Cookie("oauth_state")
		if err != nil || subtle.ConstantTimeCompare(
			[]byte(stateCookie.Value),
			[]byte(r.URL.Query().Get("state")),
		) != 1 {
			slog.Warn("oauth state mismatch", "has_cookie", err == nil)
			clearAndRedirect(w, r, "auth_expired")
			return
		}

		http.SetCookie(w, &http.Cookie{
			Name:     "oauth_state",
			Value:    "",
			MaxAge:   -1,
			Path:     "/",
			HttpOnly: true,
			Secure:   true,
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

		tn, err := tenant.UpsertTenant(r.Context(), db, user.ID, user.Login, user.Email, user.AvatarURL, user.Name, user.Company, user.Location, user.Bio)
		if err != nil {
			slog.Error("upserting tenant", "error", err)
			clearAndRedirect(w, r, "auth_failed")
			return
		}

		slog.Info("user signed in", "username", tn.Username, "tenant_id", tn.ID)

		sessionToken, err := tenant.CreateSession(r.Context(), db, tn.ID, sessionTTL)
		if err != nil {
			slog.Error("creating session", "error", err)
			clearAndRedirect(w, r, "auth_failed")
			return
		}

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
		renderTemplate(w, "tos.html", pageData{Title: "Terms of Service", Plans: plan.All})
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
			slog.Error("listing "+label, "error", err)
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

func addRepoHandler(db *sql.DB, trigger *importTrigger) http.HandlerFunc {
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
		install, err := tenant.GetInstallationForOrg(r.Context(), db, tn.ID, org)
		if err != nil {
			slog.Error("checking installation", "org", org, "error", err)
			http.Error(w, "error checking installation", http.StatusInternalServerError)
			return
		}
		if install == nil {
			installs, err := tenant.GetActiveInstallations(r.Context(), db, tn.ID)
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
			http.Error(w, "No GitHub App installation found. Go to https://github.com/apps/DevPulseThingz and click Configure to install the app.",
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
			triggerCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
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

	resp, err := httpClient.Do(req) //nolint:gosec // constant base URL
	if err != nil {
		return false
	}
	resp.Body.Close()

	return resp.StatusCode == http.StatusOK
}

func repoOverviewHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
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
		writeJSON(w, http.StatusOK, overview)
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

		resp, err := httpClient.Do(req) //nolint:gosec // URL constructed from constant base + user query param
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
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(b)
}
