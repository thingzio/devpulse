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

package server

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// tokenQuotaSample is a single rate-limit snapshot for one installation.
type tokenQuotaSample struct {
	SampledAt      time.Time `json:"sampled_at"`
	InstallationID int64     `json:"installation_id"`
	Login          string    `json:"login"`
	QuotaLimit     int       `json:"quota_limit"`
	QuotaUsed      int       `json:"quota_used"`
}

const getTokenQuotaSamplesSQL = `
	SELECT sampled_at, installation_id, login, quota_limit, quota_used
	FROM devpulse_token_quota_sample
	WHERE sampled_at >= $1
	ORDER BY sampled_at ASC`

// getTokenQuotaSamples returns all quota samples since the given time.
func getTokenQuotaSamples(ctx context.Context, db *sql.DB, since time.Time) ([]tokenQuotaSample, error) {
	rows, err := db.QueryContext(ctx, getTokenQuotaSamplesSQL, since)
	if err != nil {
		return nil, fmt.Errorf("querying token quota samples: %w", err)
	}
	defer rows.Close()

	var out []tokenQuotaSample
	for rows.Next() {
		var s tokenQuotaSample
		if err := rows.Scan(&s.SampledAt, &s.InstallationID, &s.Login, &s.QuotaLimit, &s.QuotaUsed); err != nil {
			return nil, fmt.Errorf("scanning token quota sample: %w", err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating token quota samples: %w", err)
	}
	return out, nil
}
