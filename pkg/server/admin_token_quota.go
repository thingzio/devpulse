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
