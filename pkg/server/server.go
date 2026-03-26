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
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/thingzio/devpulse/pkg/middleware"
	"github.com/thingzio/devpulse/pkg/oauth"
	"github.com/thingzio/devpulse/pkg/tenant"
)

//go:embed templates/*.html
var templateFS embed.FS

var pageTemplates map[string]*template.Template

func init() {
	pages := []string{"landing.html", "tos.html", "dashboard.html"}
	pageTemplates = make(map[string]*template.Template, len(pages))
	for _, p := range pages {
		pageTemplates[p] = template.Must(template.ParseFS(templateFS,
			"templates/layout.html", "templates/"+p))
	}
}

const (
	sessionTTL              = 7 * 24 * time.Hour
	serverReadTimeout       = 30 * time.Second
	serverReadHeaderTimeout = 5 * time.Second
	serverWriteTimeout      = 60 * time.Second
	serverIdleTimeout       = 120 * time.Second
	serverMaxHeaderBytes    = 20
)

// Run starts the HTTP server. It blocks until a shutdown signal is received.
func Run(_ context.Context, db *sql.DB) error {
	port := os.Getenv("PORT")
	baseURL := strings.TrimRight(os.Getenv("BASE_URL"), "/")

	oauthCfg := &oauth.Config{
		ClientID:     os.Getenv("GITHUB_OAUTH_CLIENT_ID"),
		ClientSecret: os.Getenv("GITHUB_OAUTH_CLIENT_SECRET"),
		RedirectURL:  baseURL + "/auth/github/callback",
	}
	webhookSecret := os.Getenv("GITHUB_WEBHOOK_SECRET")

	mux := makeRouter(db, oauthCfg, webhookSecret)

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

	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		if err := s.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("failed to start", "error", err)
		}
	}()

	slog.Info("server started", "address", address)
	<-done

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := s.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("shutdown failed", "error", err)
	}
	return nil
}

func makeRouter(db *sql.DB, oauthCfg *oauth.Config, webhookSecret string) *http.ServeMux {
	mux := http.NewServeMux()

	// Public routes
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
	mux.Handle("POST /api/repos", wrap(addRepoHandler(db)))
	mux.Handle("DELETE /api/repos/{org}/{repo}", wrap(deleteRepoHandler(db)))
	mux.Handle("GET /api/repos/available", wrap(availableReposHandler(db)))
	mux.Handle("GET /api/installations", wrap(listInstallationsHandler(db)))

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
		appURL := os.Getenv("GITHUB_APP_URL")
		renderTemplate(w, "dashboard.html", pageData{
			Title:        "Dashboard",
			Username:     tn.Username,
			GitHubAppURL: appURL,
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

		tn, err := tenant.UpsertTenant(db, user.ID, user.Login, user.Email, user.AvatarURL)
		if err != nil {
			slog.Error("upserting tenant", "error", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		slog.Info("user signed in", "username", tn.Username, "tenant_id", tn.ID)

		sessionToken, err := tenant.CreateSession(db, tn.ID, sessionTTL)
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
			if derr := tenant.DestroySession(db, cookie.Value); derr != nil {
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
		if err := tenant.AcceptToS(db, tn.ID); err != nil {
			slog.Error("accepting tos", "error", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		slog.Info("tos accepted", "tenant_id", tn.ID, "username", tn.Username)
		http.Redirect(w, r, "/dashboard", http.StatusFound)
	}
}

func tenantListHandler(db *sql.DB, label string, queryFn func(*sql.DB, string) (any, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tn := middleware.TenantFromContext(r.Context())
		if tn == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		result, err := queryFn(db, tn.ID)
		if err != nil {
			slog.Error("listing "+label, "error", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func listReposHandler(db *sql.DB) http.HandlerFunc {
	return tenantListHandler(db, "repos", func(d *sql.DB, id string) (any, error) {
		return tenant.ListTenantRepos(d, id)
	})
}

func listInstallationsHandler(db *sql.DB) http.HandlerFunc {
	return tenantListHandler(db, "installations", func(d *sql.DB, id string) (any, error) {
		return tenant.ListInstallations(d, id)
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

		if err := tenant.AddTenantRepos(db, tn.ID, []tenant.OrgRepo{{Org: org, Repo: repo}}); err != nil {
			slog.Error("adding repo", "error", err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		w.WriteHeader(http.StatusCreated)
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

		if err := tenant.DeactivateTenantRepo(db, tn.ID, org, repo); err != nil {
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

		resp, err := http.DefaultClient.Do(req) //nolint:gosec // URL constructed from constant base + user query param
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
		tracked, _ := tenant.ListTenantRepos(db, tn.ID)
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

func writeJSON(w http.ResponseWriter, _ int, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("encoding json response", "error", err)
	}
}
