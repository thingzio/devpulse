package importer

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"os"
	"time"

	"github.com/thingzio/devpulse/pkg/data/ghutil"
	"github.com/thingzio/devpulse/pkg/data/postgres"
	"github.com/thingzio/devpulse/pkg/tenant"
)

// RunDeepReputation scores all stale contributors globally using round-robin
// token rotation across all active installations. Single task, no parallelism.
func RunDeepReputation(ctx context.Context) error {
	store, err := postgres.NewFromEnv(postgres.ImportPoolConfig())
	if err != nil {
		return fmt.Errorf("opening store: %w", err)
	}
	defer func() {
		if closeErr := store.Close(); closeErr != nil {
			slog.Error("closing store", "error", closeErr)
		}
	}()

	db := store.DB()
	start := time.Now()

	ghAppConfig, ghAppErr := tenant.LoadGitHubAppConfig()
	if ghAppErr != nil {
		slog.Warn("github app config not available", "error", ghAppErr)
	}

	executionID := os.Getenv("CLOUD_RUN_EXECUTION")
	if executionID == "" {
		executionID = fmt.Sprintf("local-%d", time.Now().Unix())
	}

	slog.Info("deep reputation worker starting", "execution", executionID)

	pool, err := collectTokenPool(ctx, db, ghAppConfig)
	if err != nil {
		return fmt.Errorf("collecting token pool: %w", err)
	}

	slog.Info("token pool ready", "tokens", pool.Size())

	// Score all stale users globally (nil org/repo = all users via COALESCE).
	res, err := store.ImportDeepReputation(ctx, pool.Token, deepReputationDefaultLimit, 0, nil, nil)
	if err != nil && ghutil.WaitForRateReset(ctx, err) {
		res, err = store.ImportDeepReputation(ctx, pool.Token, deepReputationDefaultLimit, 0, nil, nil)
	}
	if err != nil {
		return fmt.Errorf("deep reputation scoring: %w", err)
	}

	slog.Info("deep reputation worker complete",
		"scored", res.Scored,
		"errors", res.Errors,
		"tokens", pool.Size(),
		"token_usage", pool.UsageCounts(),
		"duration", time.Since(start).String())

	return nil
}

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
			slog.Info("minted installation token",
				"tenant_id", tn.ID,
				"installation_id", inst.ID,
				"login", inst.Login,
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
