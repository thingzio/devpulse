package server

import (
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

	"github.com/thingzio/devpulse/pkg/data"
	"github.com/thingzio/devpulse/pkg/middleware"
	"github.com/thingzio/devpulse/pkg/oauth"
	"github.com/thingzio/devpulse/pkg/tenant"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static/*
var staticFS embed.FS

var pageTemplates map[string]*template.Template

func init() {
	// Simple pages using layout.html
	simplePages := []string{"landing.html", "tos.html"}
	pageTemplates = make(map[string]*template.Template, len(simplePages)+1)
	for _, p := range simplePages {
		pageTemplates[p] = template.Must(template.ParseFS(templateFS,
			"templates/layout.html", "templates/"+p))
	}
	// Dashboard uses the old header/home/footer pattern
	pageTemplates["home.html"] = template.Must(template.ParseFS(templateFS,
		"templates/header.html", "templates/home.html", "templates/footer.html"))
}

const (
	sessionTTL              = 7 * 24 * time.Hour
	serverReadTimeout       = 30 * time.Second
	serverReadHeaderTimeout = 5 * time.Second
	serverWriteTimeout      = 60 * time.Second
	serverIdleTimeout       = 120 * time.Second
	serverMaxHeaderBytes    = 20
	externalHTTPTimeout     = 10 * time.Second
)

// httpClient is used for all outbound HTTP calls (GitHub API, etc.)
var httpClient = &http.Client{Timeout: externalHTTPTimeout}

var (
	version = "dev"
	commit  = ""
	date    = ""
)

// SetVersion sets build info for templates.
func SetVersion(v, c, d string) {
	version, commit, date = v, c, d
}

// Run starts the HTTP server. It blocks until the context is canceled.
func Run(ctx context.Context, db *sql.DB, store data.Store) error {
	port := os.Getenv("PORT")
	baseURL := strings.TrimRight(os.Getenv("BASE_URL"), "/")

	oauthCfg := &oauth.Config{
		ClientID:     os.Getenv("GITHUB_OAUTH_CLIENT_ID"),
		ClientSecret: os.Getenv("GITHUB_OAUTH_CLIENT_SECRET"),
		RedirectURL:  baseURL + "/auth/github/callback",
	}
	webhookSecret := os.Getenv("GITHUB_WEBHOOK_SECRET")

	mux := makeRouter(db, store, oauthCfg, webhookSecret)

	address := fmt.Sprintf("0.0.0.0:%s", port)
	s := &http.Server{
		Addr:              address,
		Handler:           mux,
		ReadTimeout:       serverReadTimeout,
		ReadHeaderTimeout: serverReadHeaderTimeout,
		WriteTimeout:      serverWriteTimeout,
		IdleTimeout:       serverIdleTimeout,
		MaxHeaderBytes:    1 << serverMaxHeaderBytes,
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
	return nil
}

func makeRouter(db *sql.DB, store data.Store, oauthCfg *oauth.Config, webhookSecret string) *http.ServeMux {
	mux := http.NewServeMux()

	// Static assets
	mux.Handle("GET /static/", http.FileServer(http.FS(staticFS)))

	// Public routes (no auth)
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("GET /{$}", landingHandler())
	mux.HandleFunc("GET /auth/github", oauthStartHandler(oauthCfg))
	mux.HandleFunc("GET /auth/github/callback", oauthCallbackHandler(db, oauthCfg))
	mux.HandleFunc("POST /webhook/github", WebhookHandler(db, webhookSecret))

	// Auth middleware
	auth := middleware.RequireAuth(db, "/auth/github")
	wrap := func(h http.HandlerFunc) http.Handler {
		return auth(h)
	}

	// Authenticated routes
	mux.Handle("GET /tos", wrap(tosPageHandler()))
	mux.Handle("POST /tos/accept", wrap(tosAcceptHandler(db)))
	mux.Handle("GET /dashboard", wrap(dashboardHandler()))
	mux.Handle("POST /auth/signout", wrap(signoutHandler(db)))

	// Tenant management API
	mux.Handle("GET /api/repos", wrap(listReposHandler(db)))
	mux.Handle("GET /api/repos/overview", wrap(repoOverviewHandler(db)))
	mux.Handle("POST /api/repos", wrap(addRepoHandler(db)))
	mux.Handle("DELETE /api/repos/{org}/{repo}", wrap(deleteRepoHandler(db)))
	mux.Handle("GET /api/repos/available", wrap(availableReposHandler(db)))
	mux.Handle("GET /api/installations", wrap(listInstallationsHandler(db)))

	// Data API (chart endpoints, authenticated)
	mux.Handle("GET /data/min-date", wrap(minDateAPIHandler(store)))
	mux.Handle("GET /data/query", wrap(queryAPIHandler(store)))
	mux.Handle("GET /data/type", wrap(eventDataAPIHandler(store)))
	mux.Handle("GET /data/entity", wrap(entityDataAPIHandler(store)))
	mux.Handle("GET /data/developer", wrap(developerDataAPIHandler(store)))
	mux.Handle("POST /data/search", wrap(eventSearchAPIHandler(store)))
	mux.Handle("GET /data/entity/developers", wrap(entityDevelopersAPIHandler(store)))
	mux.Handle("GET /data/developer/search", wrap(developerSearchAPIHandler(store)))
	mux.Handle("GET /data/insights/summary", wrap(insightsSummaryAPIHandler(store)))
	mux.Handle("GET /data/insights/daily-activity", wrap(insightsDailyActivityAPIHandler(store)))
	mux.Handle("GET /data/insights/retention", wrap(insightsRetentionAPIHandler(store)))
	mux.Handle("GET /data/insights/pr-ratio", wrap(insightsPRRatioAPIHandler(store)))
	mux.Handle("GET /data/insights/time-to-merge", wrap(insightsTimeToMergeAPIHandler(store)))
	mux.Handle("GET /data/insights/time-to-close", wrap(insightsTimeToCloseAPIHandler(store)))
	mux.Handle("GET /data/insights/time-to-restore", wrap(insightsTimeToRestoreAPIHandler(store)))
	mux.Handle("GET /data/insights/review-latency", wrap(insightsReviewLatencyAPIHandler(store)))
	mux.Handle("GET /data/insights/forks-and-activity", wrap(insightsForksAndActivityAPIHandler(store)))
	mux.Handle("GET /data/insights/repo-meta", wrap(insightsRepoMetaAPIHandler(store)))
	mux.Handle("GET /data/insights/repo-overview", wrap(insightsRepoOverviewAPIHandler(store)))
	mux.Handle("GET /data/insights/repo-metric-history", wrap(insightsRepoMetricHistoryAPIHandler(store)))
	mux.Handle("GET /data/insights/change-failure-rate", wrap(insightsChangeFailureRateAPIHandler(store)))
	mux.Handle("GET /data/insights/pr-size", wrap(insightsPRSizeAPIHandler(store)))
	mux.Handle("GET /data/insights/contributor-momentum", wrap(insightsContributorMomentumAPIHandler(store)))
	mux.Handle("GET /data/insights/contributor-funnel", wrap(insightsContributorFunnelAPIHandler(store)))
	mux.Handle("GET /data/insights/contributor-profile", wrap(insightsContributorProfileAPIHandler(store)))
	mux.Handle("GET /data/insights/release-cadence", wrap(insightsReleaseCadenceAPIHandler(store)))
	mux.Handle("GET /data/insights/release-downloads", wrap(insightsReleaseDownloadsAPIHandler(store)))
	mux.Handle("GET /data/insights/release-downloads-by-tag", wrap(insightsReleaseDownloadsByTagAPIHandler(store)))
	mux.Handle("GET /data/insights/container-activity", wrap(insightsContainerActivityAPIHandler(store)))
	mux.Handle("GET /data/insights/reputation", wrap(insightsReputationAPIHandler(store)))
	mux.Handle("GET /data/insights/issue-ratio", wrap(insightsIssueRatioAPIHandler(store)))
	mux.Handle("GET /data/insights/time-to-first-response", wrap(insightsTimeToFirstResponseAPIHandler(store)))
	mux.Handle("GET /data/insights/generated", wrap(insightsGeneratedAPIHandler(store)))

	return mux
}

func renderTemplate(w http.ResponseWriter, name string, data any) {
	t, ok := pageTemplates[name]
	if !ok {
		slog.Error("template not found", "name", name)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := t.ExecuteTemplate(w, "layout.html", data); err != nil {
		slog.Error("rendering template", "name", name, "error", err)
	}
}

type pageData struct {
	Title        string
	Username     string
	GitHubAppURL string
}

func landingHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		renderTemplate(w, "landing.html", pageData{Title: "Home"})
	}
}

func dashboardHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tn := middleware.TenantFromContext(r.Context())
		if tn == nil {
			http.Redirect(w, r, "/auth/github", http.StatusFound)
			return
		}
		t := pageTemplates["home.html"]
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := t.ExecuteTemplate(w, "home", map[string]any{
			"base_path":     "",
			"version":       version,
			"commit":        commit,
			"build_date":    date,
			"period_months": 6,
			"username":      tn.Username,
		}); err != nil {
			slog.Error("rendering dashboard", "error", err)
		}
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
	return func(w http.ResponseWriter, r *http.Request) {
		stateCookie, err := r.Cookie("oauth_state")
		if err != nil || subtle.ConstantTimeCompare(
			[]byte(stateCookie.Value),
			[]byte(r.URL.Query().Get("state")),
		) != 1 {
			http.Error(w, "invalid state", http.StatusBadRequest)
			return
		}

		code := r.URL.Query().Get("code")
		token, err := oauth.ExchangeCode(r.Context(), cfg, code)
		if err != nil {
			slog.Error("oauth exchange failed", "error", err)
			http.Error(w, "authentication failed", http.StatusBadRequest)
			return
		}

		user, err := oauth.FetchUser(r.Context(), cfg, token)
		if err != nil {
			slog.Error("fetching github user", "error", err)
			http.Error(w, "authentication failed", http.StatusInternalServerError)
			return
		}

		tn, err := tenant.UpsertTenant(r.Context(), db, user.ID, user.Login, user.Email, user.AvatarURL)
		if err != nil {
			slog.Error("upserting tenant", "error", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		slog.Info("user signed in", "username", tn.Username, "tenant_id", tn.ID)

		sessionToken, err := tenant.CreateSession(r.Context(), db, tn.ID, sessionTTL)
		if err != nil {
			slog.Error("creating session", "error", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
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
		renderTemplate(w, "tos.html", pageData{Title: "Terms of Service"})
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

		// Verify the repo is publicly accessible before adding.
		// This prevents users from gaining access to private repo data
		// imported by another tenant.
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
		months := queryParamInt(r, "m", 6)
		overview, err := tenant.GetOverview(r.Context(), db, tn.ID, months)
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

		// Search GitHub public repos
		ghURL := fmt.Sprintf("https://api.github.com/search/repositories?q=%s&per_page=10",
			url.QueryEscape(query))
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
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			writeJSON(w, http.StatusOK, []tenant.OrgRepo{})
			return
		}

		// Filter out already-tracked repos
		tracked, _ := tenant.ListTenantRepos(r.Context(), db, tn.ID)
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
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("encoding json response", "error", err)
	}
}
