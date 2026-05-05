package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/thingzio/devpulse/pkg/data"
)

const (
	selectMaxEventTimeSQL  = `SELECT MAX(created_at) FROM devpulse_event WHERE org = $1 AND repo = $2`
	selectHasForkEventsSQL = `SELECT EXISTS(SELECT 1 FROM devpulse_event WHERE org = $1 AND repo = $2 AND type = 'fork')`
)

// HasForkEvents reports whether the events table contains at least one
// fork row for the repo. Used by the import skip-check so a repo whose
// fork data is missing (e.g. just after the migration-025 wipe) gets
// re-imported even when pushed_at hasn't moved.
func (s *Store) HasForkEvents(ctx context.Context, org, repo string) (bool, error) {
	if s.db == nil {
		return false, data.ErrDBNotInitialized
	}
	var exists bool
	if err := s.db.QueryRowContext(ctx, selectHasForkEventsSQL, org, repo).Scan(&exists); err != nil {
		return false, fmt.Errorf("checking fork events for %s/%s: %w", org, repo, err)
	}
	return exists, nil
}

func (s *Store) GetMaxEventTime(ctx context.Context, org, repo string) (time.Time, error) {
	if s.db == nil {
		return time.Time{}, data.ErrDBNotInitialized
	}

	var raw sql.NullString
	if err := s.db.QueryRowContext(ctx, selectMaxEventTimeSQL, org, repo).Scan(&raw); err != nil {
		return time.Time{}, fmt.Errorf("querying max event time for %s/%s: %w", org, repo, err)
	}

	if !raw.Valid || raw.String == "" {
		return time.Time{}, nil
	}

	// created_at may be "YYYY-MM-DD" or full RFC3339 "YYYY-MM-DDTHH:MM:SSZ".
	t, err := time.Parse("2006-01-02T15:04:05Z", raw.String)
	if err != nil {
		t, err = time.Parse("2006-01-02", raw.String)
	}
	if err != nil {
		return time.Time{}, fmt.Errorf("parsing max event time %q for %s/%s: %w", raw.String, org, repo, err)
	}

	return t, nil
}
