package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/thingzio/devpulse/pkg/tenant"
)

// WebhookHandler handles GitHub App webhook events.
func WebhookHandler(db *sql.DB, webhookSecret string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if webhookSecret == "" {
			slog.Error("webhook secret not configured")
			http.Error(w, "webhook not configured", http.StatusServiceUnavailable)
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1MB limit
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		sig := r.Header.Get("X-Hub-Signature-256")
		if !verifyWebhookSignature(body, sig, webhookSecret) {
			http.Error(w, "invalid signature", http.StatusUnauthorized)
			return
		}

		event := r.Header.Get("X-GitHub-Event")

		var handleErr error
		switch event {
		case "installation":
			slog.Info("webhook received", "event", event)
			handleErr = handleInstallationEvent(r.Context(), db, body)
		case "installation_repositories":
			slog.Info("webhook received", "event", event)
			handleErr = handleInstallationReposEvent(r.Context(), db, body)
		default:
			slog.Debug("webhook ignored", "event", event)
		}

		if handleErr != nil {
			slog.Error("webhook processing failed", "event", event, "error", handleErr)
			writeError(w, http.StatusInternalServerError, "webhook processing failed")
			return
		}

		w.WriteHeader(http.StatusOK)
	}
}

func verifyWebhookSignature(payload []byte, signature, secret string) bool {
	if !strings.HasPrefix(signature, "sha256=") {
		return false
	}
	sig, err := hex.DecodeString(strings.TrimPrefix(signature, "sha256="))
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	return hmac.Equal(sig, mac.Sum(nil))
}

func handleInstallationEvent(ctx context.Context, db *sql.DB, body []byte) error {
	var payload struct {
		Action       string `json:"action"`
		Installation struct {
			ID      int64 `json:"id"`
			Account struct {
				Login string `json:"login"`
				Type  string `json:"type"`
			} `json:"account"`
		} `json:"installation"`
		Sender struct {
			ID int64 `json:"id"`
		} `json:"sender"`
		Repositories []struct {
			FullName string `json:"full_name"`
		} `json:"repositories"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return fmt.Errorf("parsing installation webhook: %w", err)
	}

	slog.Info("installation event",
		"action", payload.Action,
		"installation_id", payload.Installation.ID,
		"sender_id", payload.Sender.ID,
	)

	switch payload.Action {
	case "created":
		tn, err := tenant.GetTenantByGitHubID(ctx, db, payload.Sender.ID)
		if err != nil {
			return fmt.Errorf("tenant not found for installation (sender_id=%d): %w", payload.Sender.ID, err)
		}

		if err := tenant.SaveInstallation(ctx, db, tn.ID, payload.Installation.ID,
			payload.Installation.Account.Type, payload.Installation.Account.Login, nil); err != nil {
			return fmt.Errorf("saving installation: %w", err)
		}

		slog.Info("installation created, repos must be added manually via dashboard",
			"tenant_id", tn.ID,
			"installation_id", payload.Installation.ID,
			"repos_in_payload", len(payload.Repositories),
		)

	case "deleted", "suspend":
		if err := tenant.SuspendInstallation(ctx, db, payload.Installation.ID); err != nil {
			return fmt.Errorf("suspending installation: %w", err)
		}
	}

	return nil
}

func handleInstallationReposEvent(ctx context.Context, db *sql.DB, body []byte) error {
	var payload struct {
		Installation struct {
			ID int64 `json:"id"`
		} `json:"installation"`
		Sender struct {
			ID int64 `json:"id"`
		} `json:"sender"`
		RepositoriesRemoved []struct {
			FullName string `json:"full_name"`
		} `json:"repositories_removed"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return fmt.Errorf("parsing installation_repositories webhook: %w", err)
	}

	// Only process removals — repos must be added manually via the dashboard.
	if len(payload.RepositoriesRemoved) == 0 {
		slog.Debug("installation_repositories webhook with no removals, skipping")
		return nil
	}

	tn, err := tenant.GetTenantByGitHubID(ctx, db, payload.Sender.ID)
	if err != nil {
		return fmt.Errorf("tenant not found (sender_id=%d): %w", payload.Sender.ID, err)
	}

	for _, r := range payload.RepositoriesRemoved {
		parts := strings.SplitN(r.FullName, "/", 2)
		if len(parts) == 2 {
			if err := tenant.DeactivateTenantRepo(ctx, db, tn.ID, parts[0], parts[1]); err != nil {
				return fmt.Errorf("deactivating repo %s: %w", r.FullName, err)
			}
		}
	}

	return nil
}
