package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
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
		slog.Info("webhook received", "event", event)

		switch event {
		case "installation":
			handleInstallationEvent(db, body)
		case "installation_repositories":
			handleInstallationReposEvent(db, body)
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

func handleInstallationEvent(db *sql.DB, body []byte) {
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
		slog.Error("parsing installation webhook", "error", err)
		return
	}

	slog.Info("installation event",
		"action", payload.Action,
		"installation_id", payload.Installation.ID,
		"sender_id", payload.Sender.ID,
	)

	ctx := context.Background()

	switch payload.Action {
	case "created":
		tn, err := tenant.GetTenantByGitHubID(ctx, db, payload.Sender.ID)
		if err != nil {
			slog.Error("tenant not found for installation", "sender_id", payload.Sender.ID, "error", err)
			return
		}

		if err := tenant.SaveInstallation(ctx, db, tn.ID, payload.Installation.ID,
			payload.Installation.Account.Type, payload.Installation.Account.Login, nil); err != nil {
			slog.Error("saving installation", "error", err)
			return
		}

		repos := parseRepoNames(payload.Repositories)
		if len(repos) > 0 {
			if err := tenant.AddTenantRepos(ctx, db, tn.ID, repos); err != nil {
				slog.Error("adding repos from installation", "error", err)
			}
		}

	case "deleted", "suspend":
		if err := tenant.SuspendInstallation(ctx, db, payload.Installation.ID); err != nil {
			slog.Error("suspending installation", "error", err)
		}
	}
}

func handleInstallationReposEvent(db *sql.DB, body []byte) {
	var payload struct {
		Installation struct {
			ID int64 `json:"id"`
		} `json:"installation"`
		Sender struct {
			ID int64 `json:"id"`
		} `json:"sender"`
		RepositoriesAdded []struct {
			FullName string `json:"full_name"`
		} `json:"repositories_added"`
		RepositoriesRemoved []struct {
			FullName string `json:"full_name"`
		} `json:"repositories_removed"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		slog.Error("parsing installation_repositories webhook", "error", err)
		return
	}

	ctx := context.Background()

	tn, err := tenant.GetTenantByGitHubID(ctx, db, payload.Sender.ID)
	if err != nil {
		slog.Error("tenant not found", "sender_id", payload.Sender.ID, "error", err)
		return
	}

	if len(payload.RepositoriesAdded) > 0 {
		repos := parseRepoNames(payload.RepositoriesAdded)
		if err := tenant.AddTenantRepos(ctx, db, tn.ID, repos); err != nil {
			slog.Error("adding repos", "error", err)
		}
	}

	for _, r := range payload.RepositoriesRemoved {
		parts := strings.SplitN(r.FullName, "/", 2)
		if len(parts) == 2 {
			if err := tenant.DeactivateTenantRepo(ctx, db, tn.ID, parts[0], parts[1]); err != nil {
				slog.Error("deactivating repo", "repo", r.FullName, "error", err)
			}
		}
	}
}

func parseRepoNames(repos []struct {
	FullName string `json:"full_name"`
}) []tenant.OrgRepo {
	result := make([]tenant.OrgRepo, 0, len(repos))
	for _, r := range repos {
		parts := strings.SplitN(r.FullName, "/", 2)
		if len(parts) == 2 {
			result = append(result, tenant.OrgRepo{Org: parts[0], Repo: parts[1]})
		}
	}
	return result
}
