package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/thingzio/devpulse/pkg/data/postgres"
	"github.com/thingzio/devpulse/pkg/logging"
	"github.com/thingzio/devpulse/pkg/plan"
	"github.com/thingzio/devpulse/pkg/tenant"
)

var (
	version = "v0.0.1-default"
	commit  = ""
	date    = ""
)

const (
	selectTenantByUsernameSQL = `SELECT id FROM tenant WHERE username = $1`
	clearUpgradeRequestSQL    = `UPDATE tenant SET upgrade_requested_at = NULL, updated_at = NOW() WHERE id = $1`
	listTenantsSQL            = `SELECT username, plan, max_repos, max_events_per_week, created_at FROM tenant ORDER BY created_at`
)

type upgradeRequest struct {
	Username string `json:"username"`
	Plan     string `json:"plan"`
}

type upgradeResponse struct {
	Username         string `json:"username"`
	Plan             string `json:"plan"`
	MaxRepos         int    `json:"max_repos"`
	MaxEventsPerWeek int    `json:"max_events_per_week"`
}

type tenantSummary struct {
	Username         string `json:"username"`
	Plan             string `json:"plan"`
	MaxRepos         int    `json:"max_repos"`
	MaxEventsPerWeek int    `json:"max_events_per_week"`
	CreatedAt        string `json:"created_at"`
}

func main() {
	logging.SetupLogger()

	slog.Info("starting devpulse-admin",
		"version", version,
		"commit", commit,
		"date", date,
	)

	store, err := postgres.NewFromEnv()
	if err != nil {
		slog.Error("fatal error", "error", fmt.Errorf("opening store: %w", err))
		os.Exit(1)
	}
	defer store.Close()

	db := store.DB()

	mux := http.NewServeMux()

	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	mux.HandleFunc("GET /tenants", func(w http.ResponseWriter, r *http.Request) {
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
			if err := rows.Scan(&t.Username, &t.Plan, &t.MaxRepos, &t.MaxEventsPerWeek, &createdAt); err != nil {
				slog.Error("scanning tenant", "error", err)
				http.Error(w, "error scanning tenant", http.StatusInternalServerError)
				return
			}
			t.CreatedAt = createdAt.Format("2006-01-02")
			tenants = append(tenants, t)
		}
		if err := rows.Err(); err != nil {
			slog.Error("iterating tenants", "error", err)
			http.Error(w, "error iterating tenants", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(tenants); err != nil {
			slog.Error("encoding tenants", "error", err)
		}
	})

	mux.HandleFunc("POST /upgrade", func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
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
			http.Error(w, fmt.Sprintf("invalid plan: %s (must be free, pro, or enterprise)", req.Plan), http.StatusBadRequest)
			return
		}

		// Find tenant by username — query all tenants isn't ideal but admin is low-volume.
		var tenantID string
		err := db.QueryRowContext(r.Context(), selectTenantByUsernameSQL, req.Username).Scan(&tenantID)
		if err != nil {
			http.Error(w, fmt.Sprintf("tenant not found: %s", req.Username), http.StatusNotFound)
			return
		}

		if err := tenant.UpdatePlan(r.Context(), db, tenantID, req.Plan, limits.MaxRepos, limits.MaxEventsPerWeek); err != nil {
			slog.Error("updating plan", "error", err)
			http.Error(w, "error updating plan", http.StatusInternalServerError)
			return
		}

		// Clear upgrade request
		if _, err := db.ExecContext(r.Context(), clearUpgradeRequestSQL, tenantID); err != nil {
			slog.Warn("clearing upgrade request", "error", err)
		}

		slog.Info("tenant upgraded",
			"username", req.Username,
			"plan", req.Plan,
			"max_repos", limits.MaxRepos,
			"max_events_per_week", limits.MaxEventsPerWeek,
		)

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(upgradeResponse{
			Username:         req.Username,
			Plan:             req.Plan,
			MaxRepos:         limits.MaxRepos,
			MaxEventsPerWeek: limits.MaxEventsPerWeek,
		}); err != nil {
			slog.Error("encoding upgrade response", "error", err)
		}
	})

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	srv := &http.Server{
		Addr:         ":" + port,
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		slog.Info("admin server started", "address", "0.0.0.0:"+port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
		slog.Info("shutting down")
	case err := <-errCh:
		slog.Error("server error", "error", err)
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
		return
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
}
