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
	"github.com/thingzio/devpulse/pkg/tenant"
)

var (
	version = "v0.0.1-default"
	commit  = ""
	date    = ""
)

var planLimits = map[string][2]int{
	"free":       {5, 2000},
	"pro":        {25, 20000},
	"enterprise": {100, 100000},
}

type upgradeRequest struct {
	Username string `json:"username"`
	Plan     string `json:"plan"`
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

	mux.HandleFunc("POST /upgrade", func(w http.ResponseWriter, r *http.Request) {
		var req upgradeRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}

		if req.Username == "" {
			http.Error(w, "username is required", http.StatusBadRequest)
			return
		}

		limits, ok := planLimits[req.Plan]
		if !ok {
			http.Error(w, fmt.Sprintf("invalid plan: %s (must be free, pro, or enterprise)", req.Plan), http.StatusBadRequest)
			return
		}

		// Find tenant by username — query all tenants isn't ideal but admin is low-volume.
		var tenantID string
		err := db.QueryRowContext(r.Context(),
			"SELECT id FROM tenant WHERE username = $1", req.Username).Scan(&tenantID)
		if err != nil {
			http.Error(w, fmt.Sprintf("tenant not found: %s", req.Username), http.StatusNotFound)
			return
		}

		if err := tenant.UpdatePlan(r.Context(), db, tenantID, req.Plan, limits[0], limits[1]); err != nil {
			slog.Error("updating plan", "error", err)
			http.Error(w, "error updating plan", http.StatusInternalServerError)
			return
		}

		// Clear upgrade request
		if _, err := db.ExecContext(r.Context(),
			"UPDATE tenant SET upgrade_requested_at = NULL, updated_at = NOW() WHERE id = $1", tenantID); err != nil {
			slog.Warn("clearing upgrade request", "error", err)
		}

		slog.Info("tenant upgraded",
			"username", req.Username,
			"plan", req.Plan,
			"max_repos", limits[0],
			"max_events_per_week", limits[1],
		)

		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"username":%q,"plan":%q,"max_repos":%d,"max_events_per_week":%d}`,
			req.Username, req.Plan, limits[0], limits[1])
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

	go func() {
		slog.Info("admin server started", "address", "0.0.0.0:"+port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	slog.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
}
