package main

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/mchmarny/devpulse/pkg/middleware"
	"github.com/mchmarny/devpulse/pkg/oauth"
	"github.com/mchmarny/devpulse/pkg/tenant"
	urfave "github.com/urfave/cli/v3"
)

const (
	sessionTTL              = 7 * 24 * time.Hour
	serverReadTimeout       = 30 * time.Second
	serverReadHeaderTimeout = 5 * time.Second
	serverWriteTimeout      = 60 * time.Second
	serverIdleTimeout       = 120 * time.Second
	serverMaxHeaderBytes    = 20
)

var serveCmd = &urfave.Command{
	Name:  "serve",
	Usage: "Start the SaaS HTTP server",
	Flags: []urfave.Flag{
		&urfave.IntFlag{
			Name:    "port",
			Value:   8080,
			Sources: urfave.EnvVars("PORT"),
		},
		&urfave.StringFlag{
			Name:    "github-oauth-client-id",
			Sources: urfave.EnvVars("GITHUB_OAUTH_CLIENT_ID"),
		},
		&urfave.StringFlag{
			Name:    "github-oauth-client-secret",
			Sources: urfave.EnvVars("GITHUB_OAUTH_CLIENT_SECRET"),
		},
		&urfave.StringFlag{
			Name:    "github-webhook-secret",
			Sources: urfave.EnvVars("GITHUB_WEBHOOK_SECRET"),
		},
		&urfave.StringFlag{
			Name:    "base-url",
			Usage:   "Public base URL (e.g. https://devpulse.thingz.io)",
			Sources: urfave.EnvVars("BASE_URL"),
		},
	},
	Action: cmdServe,
}

func cmdServe(_ context.Context, cmd *urfave.Command) error {
	dsn := cmd.Root().String("db")
	store, err := openSaaSStore(dsn)
	if err != nil {
		return err
	}
	defer store.Close()

	db := store.DB()
	port := cmd.Int("port")
	baseURL := strings.TrimRight(cmd.String("base-url"), "/")

	oauthCfg := &oauth.Config{
		ClientID:     cmd.String("github-oauth-client-id"),
		ClientSecret: cmd.String("github-oauth-client-secret"),
		RedirectURL:  baseURL + "/auth/github/callback",
	}
	webhookSecret := cmd.String("github-webhook-secret")

	mux := makeSaaSRouter(db, oauthCfg, webhookSecret)

	address := fmt.Sprintf("0.0.0.0:%d", port)
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

	slog.Info("started", "address", address)
	<-done

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := s.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("shutdown failed", "error", err)
	}
	return nil
}

func makeSaaSRouter(db *sql.DB, oauthCfg *oauth.Config, webhookSecret string) *http.ServeMux {
	mux := http.NewServeMux()

	// Public routes
	mux.HandleFunc("GET /{$}", landingHandler())
	mux.HandleFunc("GET /auth/github", oauthStartHandler(oauthCfg))
	mux.HandleFunc("GET /auth/github/callback", oauthCallbackHandler(db, oauthCfg))
	mux.HandleFunc("POST /webhook/github", webhookHandler(db, webhookSecret))

	// Auth middleware
	auth := middleware.RequireAuth(db, "/auth/github")
	wrap := func(h http.HandlerFunc) http.Handler {
		return auth(h)
	}

	// Authenticated routes
	mux.Handle("GET /dashboard", wrap(dashboardHandler()))
	mux.Handle("POST /auth/signout", wrap(signoutHandler(db)))

	// Tenant management API
	mux.Handle("GET /api/repos", wrap(listReposHandler(db)))
	mux.Handle("POST /api/repos", wrap(addRepoHandler(db)))
	mux.Handle("GET /api/installations", wrap(listInstallationsHandler(db)))

	// TODO: wire existing data API handlers from pkg/cli with RLS scoping

	return mux
}

func landingHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<!DOCTYPE html><html><head><title>DevPulse</title></head>
<body><h1>DevPulse</h1><p>GitHub project analytics.</p>
<a href="/auth/github">Sign in with GitHub</a></body></html>`)
	}
}

func dashboardHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tn := middleware.TenantFromContext(r.Context())
		if tn == nil {
			http.Redirect(w, r, "/auth/github", http.StatusFound)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, `<!DOCTYPE html><html><head><title>DevPulse Dashboard</title></head>
<body><h1>Dashboard</h1><p>Welcome, %s</p>
<a href="/api/repos">Your repos</a> |
<form method="POST" action="/auth/signout" style="display:inline"><button>Sign out</button></form>
</body></html>`, tn.Username)
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
		if cookie, err := r.Cookie("__Host-session"); err == nil {
			if derr := tenant.DestroySession(db, cookie.Value); derr != nil {
				slog.Debug("destroying session", "error", derr)
			}
		}
		middleware.ClearSessionCookie(w)
		http.Redirect(w, r, "/", http.StatusFound)
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

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("encoding json response", "error", err)
	}
}
