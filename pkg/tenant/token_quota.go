package tenant

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

//nolint:gosec // SQL constant, not a credential
const recordTokenQuotaSampleSQL = `
	INSERT INTO devpulse_token_quota_sample (installation_id, login, quota_limit, quota_used)
	VALUES ($1, $2, $3, $4)`

// RecordTokenQuotaSample writes a single quota snapshot to the time-series table.
func RecordTokenQuotaSample(ctx context.Context, db *sql.DB, installationID int64, login string, limit, used int) error {
	if _, err := db.ExecContext(ctx, recordTokenQuotaSampleSQL, installationID, login, limit, used); err != nil {
		return fmt.Errorf("recording token quota sample: %w", err)
	}
	return nil
}

//nolint:gosec // SQL constant, not a credential
const purgeTokenQuotaSamplesSQL = `
	DELETE FROM devpulse_token_quota_sample WHERE sampled_at < $1`

// PurgeOldTokenQuotaSamples deletes samples older than the given time.
func PurgeOldTokenQuotaSamples(ctx context.Context, db *sql.DB, before time.Time) (int64, error) {
	res, err := db.ExecContext(ctx, purgeTokenQuotaSamplesSQL, before)
	if err != nil {
		return 0, fmt.Errorf("purging old token quota samples: %w", err)
	}
	return res.RowsAffected()
}
