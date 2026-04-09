package importer

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"os"

	"github.com/thingzio/devpulse/pkg/data/ghutil"
	"github.com/thingzio/devpulse/pkg/tenant"
)

// collectTokenPool mints installation tokens from all active tenants and
// returns a round-robin TokenPool. Falls back to GITHUB_TOKEN env var.
func collectTokenPool(ctx context.Context, db *sql.DB, ghAppConfig *tenant.GitHubAppConfig) (*ghutil.TokenPool, error) {
	// 1. Explicit token (dev/testing)
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		return ghutil.NewTokenPool(token), nil
	}

	if ghAppConfig == nil {
		return nil, fmt.Errorf("github app config not available")
	}

	// 2. Mint tokens from all active installations across all tenants.
	tenants, err := tenant.GetActiveTenants(ctx, db)
	if err != nil {
		return nil, fmt.Errorf("getting active tenants: %w", err)
	}

	seen := make(map[int64]bool) // dedup by installation ID
	var tokens []string

	for _, tn := range tenants {
		installs, err := tenant.GetActiveInstallations(ctx, db, tn.ID)
		if err != nil || len(installs) == 0 {
			continue
		}
		for _, inst := range installs {
			if seen[inst.ID] {
				continue
			}
			seen[inst.ID] = true

			tok, err := tenant.MintInstallationToken(ctx, ghAppConfig, inst.ID)
			if err != nil {
				slog.Debug("minting token failed",
					"tenant_id", tn.ID,
					"installation_id", inst.ID,
					"error", err)
				continue
			}
			remaining := ghutil.CheckTokenQuota(ctx, tok.Token)
			if remaining >= 0 && remaining < ghutil.MinTokenQuota() {
				slog.Info("skipping token with insufficient quota",
					"installation_id", inst.ID,
					"login", inst.Login,
					"remaining", remaining)
				continue
			}
			slog.Info("minted installation token",
				"tenant_id", tn.ID,
				"installation_id", inst.ID,
				"login", inst.Login,
				"remaining", remaining,
				"expires_at", tok.ExpiresAt)
			tokens = append(tokens, tok.Token)
		}
	}

	if len(tokens) == 0 {
		return nil, fmt.Errorf("no active installations found")
	}

	rand.Shuffle(len(tokens), func(i, j int) { tokens[i], tokens[j] = tokens[j], tokens[i] })

	return ghutil.NewTokenPool(tokens...), nil
}
