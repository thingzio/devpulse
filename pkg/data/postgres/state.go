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
	"errors"
	"fmt"
	"time"

	"github.com/thingzio/devpulse/pkg/data"
)

var stateQueries = map[string]string{
	"developer": "SELECT COUNT(*) FROM devpulse_developer",
	"event":     "SELECT COUNT(*) FROM devpulse_event",
	"type":      "SELECT COUNT(DISTINCT type) FROM devpulse_event",
}

const (
	insertStateSQL = `INSERT INTO devpulse_state (query, org, repo, page, since, backfill_until)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT(query, org, repo) DO UPDATE SET page = $7, since = $8, backfill_until = $9
	`

	selectStateSQL = `SELECT since, page, backfill_until FROM devpulse_state WHERE query = $1 AND org = $2 AND repo = $3`
)

func (s *Store) GetState(ctx context.Context, query, org, repo string, min time.Time) (*data.State, error) {
	if s.db == nil {
		return nil, data.ErrDBNotInitialized
	}

	stateStmt, err := s.db.PrepareContext(ctx, selectStateSQL)
	if err != nil {
		return nil, fmt.Errorf("failed to prepare state select statement: %w", err)
	}
	defer stateStmt.Close()

	row := stateStmt.QueryRowContext(ctx, query, org, repo)

	st := &data.State{
		Since: min,
		Page:  1,
	}
	var since int64
	var backfillUntil sql.NullInt64
	err = row.Scan(&since, &st.Page, &backfillUntil)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return st, nil
		}
		return nil, fmt.Errorf("failed to scan row: %w", err)
	}

	st.Since = time.Unix(since, 0).UTC()

	if backfillUntil.Valid {
		t := time.Unix(backfillUntil.Int64, 0).UTC()
		st.BackfillUntil = &t
	}

	return st, nil
}

func (s *Store) SaveState(ctx context.Context, query, org, repo string, state *data.State) error {
	if s.db == nil {
		return data.ErrDBNotInitialized
	}

	if state == nil {
		return errors.New("state is nil")
	}

	if query == "" || org == "" || repo == "" {
		return fmt.Errorf("query: %s, org: %s, repo: %s are all required", query, org, repo)
	}

	stateStmt, err := s.db.PrepareContext(ctx, insertStateSQL)
	if err != nil {
		return fmt.Errorf("failed to prepare state insert statement: %w", err)
	}
	defer stateStmt.Close()

	since := state.Since.Unix()

	var backfillUnix sql.NullInt64
	if state.BackfillUntil != nil {
		backfillUnix = sql.NullInt64{Int64: state.BackfillUntil.Unix(), Valid: true}
	}

	if _, err = stateStmt.ExecContext(ctx, query, org, repo, state.Page, since, backfillUnix,
		state.Page, since, backfillUnix); err != nil {
		return fmt.Errorf("failed to insert state: %w", err)
	}

	return nil
}

func (s *Store) HasState(ctx context.Context, org, repo string) (bool, error) {
	if s.db == nil {
		return false, data.ErrDBNotInitialized
	}

	var count int
	err := s.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM devpulse_state WHERE org = $1 AND repo = $2",
		org, repo).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("checking state for %s/%s: %w", org, repo, err)
	}

	return count > 0, nil
}

func (s *Store) ClearState(ctx context.Context, org, repo string) error {
	if s.db == nil {
		return data.ErrDBNotInitialized
	}

	q := "DELETE FROM devpulse_state WHERE org = $1 AND repo = $2"
	if _, err := s.db.ExecContext(ctx, q, org, repo); err != nil {
		return fmt.Errorf("failed to clear state for %s/%s: %w", org, repo, err)
	}

	return nil
}

func (s *Store) GetBackfillUntil(ctx context.Context, org, repo string) (*time.Time, error) {
	if s.db == nil {
		return nil, data.ErrDBNotInitialized
	}

	var backfillUnix sql.NullInt64
	err := s.db.QueryRowContext(ctx,
		`SELECT MIN(backfill_until) FROM devpulse_state
		 WHERE org = $1 AND repo = $2 AND backfill_until IS NOT NULL`,
		org, repo).Scan(&backfillUnix)
	if err != nil {
		return nil, fmt.Errorf("querying backfill_until for %s/%s: %w", org, repo, err)
	}

	if !backfillUnix.Valid {
		return nil, nil
	}

	t := time.Unix(backfillUnix.Int64, 0).UTC()
	return &t, nil
}

func (s *Store) SaveBackfillUntil(ctx context.Context, org, repo string, until time.Time) error {
	if s.db == nil {
		return data.ErrDBNotInitialized
	}

	unixTs := until.Unix()
	_, err := s.db.ExecContext(ctx,
		`UPDATE devpulse_state SET backfill_until = $1 WHERE org = $2 AND repo = $3`,
		unixTs, org, repo)
	if err != nil {
		return fmt.Errorf("updating backfill_until for %s/%s: %w", org, repo, err)
	}

	return nil
}

func (s *Store) GetDataState(ctx context.Context) (map[string]int64, error) {
	if s.db == nil {
		return nil, data.ErrDBNotInitialized
	}

	state := make(map[string]int64)
	for k, v := range stateQueries {
		var count int64
		if err := s.db.QueryRowContext(ctx, v).Scan(&count); err != nil {
			return nil, fmt.Errorf("error getting %s count: %w", k, err)
		}
		state[k] = count
	}

	return state, nil
}
