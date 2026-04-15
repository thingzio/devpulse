package admin

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/thingzio/devpulse/pkg/config"
	"github.com/thingzio/devpulse/pkg/data/postgres"
	"github.com/thingzio/devpulse/pkg/net"
	"github.com/thingzio/devpulse/pkg/plan"
	"github.com/thingzio/devpulse/pkg/tenant"
)

const (
	portDefault       = "8080"
	readTimeout       = 10 * time.Second
	writeTimeout      = 90 * time.Second
	shutdownTimeout   = 5 * time.Second
	maxRequestBodyLen = 1 << 20
)

// Run starts the admin HTTP server and blocks until ctx is canceled.
func Run(ctx context.Context) error {
	store, err := postgres.NewFromEnv(postgres.AdminPoolConfig())
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
	mcfg := newMetricsConfig()
	rcfg := loadReportConfig()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("GET /tenants", handleListTenants(db))
	mux.HandleFunc("GET /tenant", handleGetTenant(db))
	mux.HandleFunc("POST /upgrade", handleUpgrade(db))
	mux.HandleFunc("POST /invite", handleInvite(db))
	mux.HandleFunc("POST /reset-errors", handleResetErrors(db))
	mux.HandleFunc("POST /hard-reset", handleHardReset(db))
	mux.HandleFunc("GET /summary", handleSummary(db))
	mux.HandleFunc("GET /metrics", handleMetricsReview(mcfg))
	mux.HandleFunc("POST /report", handleReport(db, mcfg, rcfg))
	mux.HandleFunc("GET /tokens", handleTokenStatus(db))

	address := "0.0.0.0:" + port

	var handler http.Handler = mux
	if config.AdminRequireIAM() {
		handler = requireIAMAuth(mux)
	}

	srv := &http.Server{
		Addr:              address,
		Handler:           handler,
		ReadTimeout:       readTimeout,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 16, // 64KB
	}

	errCh := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	slog.Info("admin server started", "address", address)

	select {
	case err := <-errCh:
		return fmt.Errorf("server failed: %w", err)
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("shutdown failed", "error", err)
	}
	return nil
}

// requireIAMAuth is defense-in-depth middleware that verifies Cloud Run IAM
// authentication headers are present. On Cloud Run, authenticated requests
// carry Authorization or X-Serverless-Authorization headers set by IAM.
// If neither is present, the request likely bypassed IAM (misconfiguration).
// The /health endpoint is exempt (Cloud Run probes do not carry auth).
func requireIAMAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			next.ServeHTTP(w, r)
			return
		}
		if r.Header.Get("Authorization") == "" && r.Header.Get("X-Serverless-Authorization") == "" {
			slog.Warn("admin request missing IAM auth header",
				"path", r.URL.Path,
				"remote", r.RemoteAddr)
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func handleListTenants(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tenants, err := tenant.ListTenantSummaries(r.Context(), db)
		if err != nil {
			slog.Error("listing tenants", "error", err)
			http.Error(w, "error listing tenants", http.StatusInternalServerError)
			return
		}

		out := make([]tenantSummary, len(tenants))
		for i, t := range tenants {
			out[i] = tenantSummary{
				Username:         t.Username,
				Email:            t.Email,
				Name:             t.Name,
				Plan:             t.Plan,
				MaxRepos:         t.MaxRepos,
				MaxEventsPerWeek: t.MaxEventsPerWeek,
				CreatedAt:        t.CreatedAt.Format("2006-01-02"),
			}
			if t.LastSignIn != nil {
				out[i].LastSignIn = t.LastSignIn.Format("2006-01-02")
			}
		}

		writeJSON(w, http.StatusOK, out)
	}
}

func handleGetTenant(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username := r.URL.Query().Get("username")
		if username == "" {
			http.Error(w, "username query parameter is required", http.StatusBadRequest)
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
			MaxRepos:         td.MaxRepos,
			MaxEventsPerWeek: td.MaxEventsPerWeek,
			CreatedAt:        td.CreatedAt.Format("2006-01-02"),
		}
		if td.LastSignIn != nil {
			out.LastSignIn = td.LastSignIn.Format("2006-01-02")
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
				DeepScored:     rd.DeepScored,
				NeverDeepScore: rd.NeverDeepScore,
				BackfillDays:   rd.BackfillDays,
				BackfillTarget: rd.BackfillTarget,
			}
			if out.MaxEventsPerWeek > 0 {
				d.WeeklyPct = float64(rd.WeeklyEvents) / float64(out.MaxEventsPerWeek) * 100
			}
			out.Repos = append(out.Repos, d)
		}

		writeJSON(w, http.StatusOK, out)
	}
}

func handleUpgrade(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyLen)
		var req upgradeRequest
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

		tenantID, err := tenant.GetTenantIDByUsername(r.Context(), db, req.Username)
		if err != nil {
			slog.Debug("tenant not found for upgrade", "username", req.Username, "error", err)
			http.Error(w, "tenant not found", http.StatusNotFound)
			return
		}

		if err := tenant.UpdatePlan(
			r.Context(), db, tenantID, req.Plan, limits.MaxRepos, limits.MaxEventsPerWeek,
		); err != nil {
			slog.Error("updating plan", "error", err)
			http.Error(w, "error updating plan", http.StatusInternalServerError)
			return
		}

		if err := tenant.ClearUpgradeRequest(r.Context(), db, tenantID); err != nil {
			slog.Warn("clearing upgrade request", "error", err)
		}

		slog.Info("tenant upgraded",
			"username", req.Username,
			"plan", req.Plan,
			"max_repos", limits.MaxRepos,
			"max_events_per_week", limits.MaxEventsPerWeek,
		)

		writeJSON(w, http.StatusOK, upgradeResponse{
			Username:         req.Username,
			Plan:             req.Plan,
			MaxRepos:         limits.MaxRepos,
			MaxEventsPerWeek: limits.MaxEventsPerWeek,
		})
	}
}

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

		ghID, err := resolveGitHubUserID(r.Context(), req.Username)
		if err != nil {
			slog.Error("resolving GitHub user", "username", req.Username, "error", err)
			http.Error(w, "GitHub user not found", http.StatusNotFound)
			return
		}

		tenantID, err := tenant.InsertMinimalTenant(r.Context(), db, ghID, req.Username)
		if err != nil {
			slog.Error("inserting tenant", "error", err)
			http.Error(w, "error creating tenant", http.StatusInternalServerError)
			return
		}

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

		writeJSON(w, http.StatusOK, inviteResponse{
			Username:         req.Username,
			GitHubID:         ghID,
			Plan:             req.Plan,
			MaxRepos:         limits.MaxRepos,
			MaxEventsPerWeek: limits.MaxEventsPerWeek,
		})
	}
}

type resetFunc func(ctx context.Context, db *sql.DB, org, repo string) (int64, error)

func handleResetErrors(db *sql.DB) http.HandlerFunc {
	return handleRepoReset(db, "reset errors", tenant.ResetImportErrorsByRepo)
}

func handleHardReset(db *sql.DB) http.HandlerFunc {
	return handleRepoReset(db, "hard reset", tenant.HardResetRepo)
}

func handleRepoReset(db *sql.DB, label string, fn resetFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyLen)
		var req resetErrorsRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}

		if req.Org == "" || req.Repo == "" {
			http.Error(w, "org and repo are required", http.StatusBadRequest)
			return
		}

		count, err := fn(r.Context(), db, req.Org, req.Repo)
		if err != nil {
			slog.Error(label, "org", req.Org, "repo", req.Repo, "error", err)
			http.Error(w, fmt.Sprintf("error: %s", label), http.StatusInternalServerError)
			return
		}

		slog.Info(label, "org", req.Org, "repo", req.Repo, "rows", count)

		writeJSON(w, http.StatusOK, resetErrorsResponse{
			Org:   req.Org,
			Repo:  req.Repo,
			Reset: count,
		})
	}
}

func resolveGitHubUserID(ctx context.Context, username string) (int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		fmt.Sprintf("https://api.github.com/users/%s", url.PathEscape(username)), nil)
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

func handleTokenStatus(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ghAppConfig, err := tenant.LoadGitHubAppConfig()
		if err != nil {
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

		writeJSON(w, http.StatusOK, results)
	}
}

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
