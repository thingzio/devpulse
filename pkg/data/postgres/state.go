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
	"developer": "SELECT COUNT(*) FROM developer",
	"event":     "SELECT COUNT(*) FROM event",
	"type":      "SELECT COUNT(DISTINCT type) FROM event",
}

const (
	insertStateSQL = `INSERT INTO state (query, org, repo, page, since) VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT(query, org, repo) DO UPDATE SET page = $6, since = $7
	`

	selectStateSQL = `SELECT since, page FROM state WHERE query = $1 AND org = $2 AND repo = $3`
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
	err = row.Scan(&since, &st.Page)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return st, nil
		}
		return nil, fmt.Errorf("failed to scan row: %w", err)
	}

	st.Since = time.Unix(since, 0).UTC()

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
	if _, err = stateStmt.ExecContext(ctx, query, org, repo, state.Page, since, state.Page, since); err != nil {
		return fmt.Errorf("failed to insert state: %w", err)
	}

	return nil
}

func (s *Store) ClearState(ctx context.Context, org, repo string) error {
	if s.db == nil {
		return data.ErrDBNotInitialized
	}

	q := "DELETE FROM state WHERE org = $1 AND repo = $2"
	if _, err := s.db.ExecContext(ctx, q, org, repo); err != nil {
		return fmt.Errorf("failed to clear state for %s/%s: %w", org, repo, err)
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
