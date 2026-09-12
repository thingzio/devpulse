// Copyright 2026 Thingz LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
// SPDX-License-Identifier: Apache-2.0

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
	// 1. Explicit token (dev/testing). This short-circuits the GitHub App
	// installation token pool, so warn loudly to avoid silent fan-out loss
	// in production when the env var leaks onto a Cloud Run task.
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		slog.Warn("GITHUB_TOKEN env set; bypassing installation token pool — intended for dev/testing only")
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
		installs, err := tenant.GetActiveInstallations(ctx, db, tn.ID, ghAppConfig.AppID)
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

	//nolint:gosec // load-balancing shuffle across the token pool, not a security decision
	rand.Shuffle(len(tokens), func(i, j int) { tokens[i], tokens[j] = tokens[j], tokens[i] })

	return ghutil.NewTokenPool(tokens...), nil
}
