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
	"log/slog"
	"time"

	"github.com/thingzio/devpulse/pkg/data/ghutil"
	"github.com/thingzio/devpulse/pkg/tenant"
)

const quotaSampleRetentionDays = 30

func purgeThreshold() time.Time {
	return time.Now().UTC().AddDate(0, 0, -quotaSampleRetentionDays)
}

// sampleTokenQuotas mints a token for each active installation and records
// a rate-limit snapshot. Errors are logged but not fatal.
func sampleTokenQuotas(ctx context.Context, db *sql.DB, ghAppConfig *tenant.GitHubAppConfig) {
	if ghAppConfig == nil {
		return
	}

	tenants, err := tenant.GetActiveTenants(ctx, db)
	if err != nil {
		slog.Warn("sampling quotas: listing tenants", "error", err)
		return
	}

	seen := make(map[int64]bool)
	var sampled int

	for _, tn := range tenants {
		installs, instErr := tenant.GetActiveInstallations(ctx, db, tn.ID, ghAppConfig.AppID)
		if instErr != nil || len(installs) == 0 {
			continue
		}
		for _, inst := range installs {
			if seen[inst.ID] {
				continue
			}
			seen[inst.ID] = true

			tok, mintErr := tenant.MintInstallationToken(ctx, ghAppConfig, inst.ID)
			if mintErr != nil {
				continue
			}

			q := ghutil.CheckTokenQuotaFull(ctx, tok.Token)
			if q == nil {
				continue
			}

			used := q.Limit - q.Remaining
			if recErr := tenant.RecordTokenQuotaSample(ctx, db, inst.ID, inst.Login, q.Limit, used); recErr != nil {
				slog.Warn("recording quota sample", "installation_id", inst.ID, "error", recErr)
				continue
			}
			sampled++
		}
	}

	if sampled > 0 {
		slog.Info("token quota samples recorded", "count", sampled)
	}

	// Purge old samples (>30 days).
	purged, err := tenant.PurgeOldTokenQuotaSamples(ctx, db, purgeThreshold())
	if err != nil {
		slog.Warn("purging old quota samples", "error", err)
	} else if purged > 0 {
		slog.Info("purged old quota samples", "count", purged)
	}
}
