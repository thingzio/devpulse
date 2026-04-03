package admin

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/thingzio/devpulse/pkg/config"
	"github.com/thingzio/devpulse/pkg/data/postgres"
	"github.com/thingzio/devpulse/pkg/plan"
	"github.com/thingzio/devpulse/pkg/tenant"
)

const (
	portDefault       = "8080"
	readWriteTimeout  = 10 * time.Second
	shutdownTimeout   = 5 * time.Second
	maxRequestBodyLen = 1 << 20

	selectTenantByUsernameSQL = `SELECT id FROM tenant WHERE username = $1`

	clearUpgradeRequestSQL = `
		UPDATE tenant SET upgrade_requested_at = NULL, updated_at = NOW()
		WHERE id = $1`

	listTenantsSQL = `
		SELECT t.username, t.plan, t.max_repos, t.max_events_per_week,
		       t.created_at, MAX(s.created_at) AS last_sign_in
		FROM tenant t
		LEFT JOIN session s ON s.tenant_id = t.id
		GROUP BY t.id
		ORDER BY t.created_at`

	getTenantDetailSQL = `
		SELECT t.id, t.username, t.email, t.plan,
		       t.max_repos, t.max_events_per_week,
		       t.created_at, MAX(s.created_at) AS last_sign_in
		FROM tenant t
		LEFT JOIN session s ON s.tenant_id = t.id
		WHERE t.username = $1
		GROUP BY t.id`

	getTenantReposSQL = `
		SELECT tr.org, tr.repo,
		       COUNT(e.type),
		       COUNT(CASE WHEN e.date >= $3 THEN 1 END),
		       COALESCE(rm.last_import_at, '')
		FROM tenant_repo tr
		LEFT JOIN repo_meta rm ON rm.org = tr.org AND rm.repo = tr.repo
		LEFT JOIN event e ON tr.org = e.org AND tr.repo = e.repo
		       AND e.date >= $2
		WHERE tr.tenant_id = $1 AND tr.active = TRUE
		GROUP BY tr.org, tr.repo, rm.last_import_at
		ORDER BY tr.org, tr.repo`
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

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("GET /tenants", handleListTenants(db))
	mux.HandleFunc("GET /tenant", handleGetTenant(db))
	mux.HandleFunc("POST /upgrade", handleUpgrade(db))

	address := "0.0.0.0:" + port
	srv := &http.Server{
		Addr:         address,
		Handler:      mux,
		ReadTimeout:  readWriteTimeout,
		WriteTimeout: readWriteTimeout,
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

func handleListTenants(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rows, err := db.QueryContext(r.Context(), listTenantsSQL)
		if err != nil {
			slog.Error("listing tenants", "error", err)
			http.Error(w, "error listing tenants", http.StatusInternalServerError)
			return
		}
		defer rows.Close()

		var tenants []tenantSummary
		for rows.Next() {
			var t tenantSummary
			var createdAt time.Time
			var lastSignIn sql.NullTime
			if err := rows.Scan(
				&t.Username, &t.Plan, &t.MaxRepos, &t.MaxEventsPerWeek,
				&createdAt, &lastSignIn,
			); err != nil {
				slog.Error("scanning tenant", "error", err)
				http.Error(w, "error scanning tenant", http.StatusInternalServerError)
				return
			}
			t.CreatedAt = createdAt.Format("2006-01-02")
			if lastSignIn.Valid {
				t.LastSignIn = lastSignIn.Time.Format("2006-01-02")
			}
			tenants = append(tenants, t)
		}
		if err := rows.Err(); err != nil {
			slog.Error("iterating tenants", "error", err)
			http.Error(w, "error iterating tenants", http.StatusInternalServerError)
			return
		}

		writeJSON(w, tenants)
	}
}

func handleGetTenant(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username := r.URL.Query().Get("username")
		if username == "" {
			http.Error(w, "username query parameter is required", http.StatusBadRequest)
			return
		}

		var td tenantDetail
		var tenantID string
		var createdAt time.Time
		var lastSignIn sql.NullTime
		var email sql.NullString
		err := db.QueryRowContext(r.Context(), getTenantDetailSQL, username).Scan(
			&tenantID, &td.Username, &email, &td.Plan,
			&td.MaxRepos, &td.MaxEventsPerWeek,
			&createdAt, &lastSignIn,
		)
		if err != nil {
			http.Error(w, fmt.Sprintf("tenant not found: %s", username), http.StatusNotFound)
			return
		}
		td.CreatedAt = createdAt.Format("2006-01-02")
		if lastSignIn.Valid {
			td.LastSignIn = lastSignIn.Time.Format("2006-01-02")
		}
		if email.Valid {
			td.Email = email.String
		}

		since := time.Now().UTC().AddDate(0, -6, 0).Format("2006-01-02")
		weekStart := tenant.StartOfWeek().Format("2006-01-02")

		rows, err := db.QueryContext(r.Context(), getTenantReposSQL, tenantID, since, weekStart)
		if err != nil {
			slog.Error("querying tenant repos", "error", err)
			http.Error(w, "error querying repos", http.StatusInternalServerError)
			return
		}
		defer rows.Close()

		for rows.Next() {
			var rd repoDetail
			var org, repo string
			if err := rows.Scan(&org, &repo, &rd.Events, &rd.WeeklyEvents, &rd.LastImport); err != nil {
				slog.Error("scanning repo", "error", err)
				http.Error(w, "error scanning repo", http.StatusInternalServerError)
				return
			}
			rd.Name = org + "/" + repo
			if td.MaxEventsPerWeek > 0 {
				rd.WeeklyPct = float64(rd.WeeklyEvents) / float64(td.MaxEventsPerWeek) * 100
			}
			td.Repos = append(td.Repos, rd)
		}
		if err := rows.Err(); err != nil {
			slog.Error("iterating repos", "error", err)
			http.Error(w, "error iterating repos", http.StatusInternalServerError)
			return
		}

		writeJSON(w, td)
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

		var tenantID string
		err := db.QueryRowContext(
			r.Context(), selectTenantByUsernameSQL, req.Username,
		).Scan(&tenantID)
		if err != nil {
			http.Error(w, fmt.Sprintf("tenant not found: %s", req.Username), http.StatusNotFound)
			return
		}

		if err := tenant.UpdatePlan(
			r.Context(), db, tenantID, req.Plan, limits.MaxRepos, limits.MaxEventsPerWeek,
		); err != nil {
			slog.Error("updating plan", "error", err)
			http.Error(w, "error updating plan", http.StatusInternalServerError)
			return
		}

		if _, err := db.ExecContext(r.Context(), clearUpgradeRequestSQL, tenantID); err != nil {
			slog.Warn("clearing upgrade request", "error", err)
		}

		slog.Info("tenant upgraded",
			"username", req.Username,
			"plan", req.Plan,
			"max_repos", limits.MaxRepos,
			"max_events_per_week", limits.MaxEventsPerWeek,
		)

		writeJSON(w, upgradeResponse{
			Username:         req.Username,
			Plan:             req.Plan,
			MaxRepos:         limits.MaxRepos,
			MaxEventsPerWeek: limits.MaxEventsPerWeek,
		})
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("encoding JSON response", "error", err)
	}
}
