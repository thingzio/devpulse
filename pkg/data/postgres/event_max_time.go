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

	var raw sql.NullTime
	if err := s.db.QueryRowContext(ctx, selectMaxEventTimeSQL, org, repo).Scan(&raw); err != nil {
		return time.Time{}, fmt.Errorf("querying max event time for %s/%s: %w", org, repo, err)
	}

	if !raw.Valid {
		return time.Time{}, nil
	}

	return raw.Time.UTC(), nil
}
