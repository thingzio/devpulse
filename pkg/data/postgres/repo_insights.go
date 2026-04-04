package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/thingzio/devpulse/pkg/data"
)

const (
	upsertRepoInsightsSQL = `INSERT INTO repo_insights (org, repo, insights_json, period_months, model, generated_at, event_count)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT(org, repo) DO UPDATE SET
			insights_json = $8, period_months = $9, model = $10, generated_at = $11, event_count = $12
	`

	selectRepoInsightsSQL = `SELECT org, repo, insights_json, period_months, model, generated_at, event_count
		FROM repo_insights
		WHERE org = COALESCE($1, org)
		  AND repo = COALESCE($2, repo)
		ORDER BY org, repo
	`

	selectRepoInsightsGeneratedAtSQL = `SELECT COALESCE(generated_at, '')
		FROM repo_insights
		WHERE org = $1 AND repo = $2
	`

	selectRepoInsightsEventCountSQL = `SELECT COALESCE(event_count, 0)
		FROM repo_insights
		WHERE org = $1 AND repo = $2
	`
)

func (s *Store) SaveRepoInsights(ctx context.Context, org, repo string, ri *data.RepoInsights) error {
	if s.db == nil {
		return data.ErrDBNotInitialized
	}

	b, err := json.Marshal(ri.Insights)
	if err != nil {
		return fmt.Errorf("marshaling insights JSON for %s/%s: %w", org, repo, err)
	}
	j := string(b)

	_, err = s.db.ExecContext(ctx, upsertRepoInsightsSQL,
		org, repo, j, ri.PeriodMonths, ri.Model, ri.GeneratedAt, ri.EventCount,
		j, ri.PeriodMonths, ri.Model, ri.GeneratedAt, ri.EventCount,
	)
	if err != nil {
		return fmt.Errorf("upserting repo insights %s/%s: %w", org, repo, err)
	}

	return nil
}

func (s *Store) GetRepoInsights(ctx context.Context, org, repo *string) ([]*data.RepoInsights, error) {
	if s.db == nil {
		return nil, data.ErrDBNotInitialized
	}

	rows, err := s.db.QueryContext(ctx, selectRepoInsightsSQL, org, repo)
	if err != nil {
		return nil, fmt.Errorf("querying repo insights: %w", err)
	}
	defer rows.Close()

	list := make([]*data.RepoInsights, 0)
	for rows.Next() {
		ri := &data.RepoInsights{}
		var j string
		if err := rows.Scan(&ri.Org, &ri.Repo, &j, &ri.PeriodMonths, &ri.Model, &ri.GeneratedAt, &ri.EventCount); err != nil {
			return nil, fmt.Errorf("scanning repo insights row: %w", err)
		}
		ri.Insights = &data.GeneratedInsights{}
		if err := json.Unmarshal([]byte(j), ri.Insights); err != nil {
			return nil, fmt.Errorf("unmarshaling insights JSON for %s/%s: %w", ri.Org, ri.Repo, err)
		}
		list = append(list, ri)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating repo insights rows: %w", err)
	}

	return list, nil
}

func (s *Store) GetRepoInsightsGeneratedAt(ctx context.Context, org, repo string) (string, error) {
	if s.db == nil {
		return "", data.ErrDBNotInitialized
	}

	var ts string
	if err := s.db.QueryRowContext(ctx, selectRepoInsightsGeneratedAtSQL, org, repo).Scan(&ts); err != nil {
		if err == sql.ErrNoRows {
			return "", nil
		}
		return "", fmt.Errorf("querying repo insights generated_at for %s/%s: %w", org, repo, err)
	}

	return ts, nil
}

func (s *Store) GetRepoInsightsEventCount(ctx context.Context, org, repo string) (int, error) {
	if s.db == nil {
		return 0, data.ErrDBNotInitialized
	}

	var count int
	if err := s.db.QueryRowContext(ctx, selectRepoInsightsEventCountSQL, org, repo).Scan(&count); err != nil {
		if err == sql.ErrNoRows {
			return 0, nil
		}
		return 0, fmt.Errorf("querying repo insights event_count for %s/%s: %w", org, repo, err)
	}

	return count, nil
}
