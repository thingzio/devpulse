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
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/google/go-github/v83/github"
	"github.com/thingzio/devpulse/pkg/data"
	"github.com/thingzio/devpulse/pkg/data/ghutil"
	"github.com/thingzio/devpulse/pkg/net"
)

const (
	// upsertRepoMetricHistorySQL: 7 params
	upsertRepoMetricHistorySQL = `INSERT INTO devpulse_repo_metric_history (org, repo, date, stars, forks)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT(org, repo, date) DO UPDATE SET
			stars = $6, forks = $7
	`

	// selectRepoMetricHistoryTpl: $1=since fixed; %s = queryBuilder for org/repo.
	// Replaces COALESCE($N, col) anti-pattern so the planner can use the
	// (org, repo, date) primary key when org/repo are provided.
	selectRepoMetricHistoryTpl = `SELECT org, repo, date::text, stars, forks
		FROM devpulse_repo_metric_history
		WHERE date >= $1
		  %s
		ORDER BY org, repo, date
	`

	// selectRepoMetricHistoryAggTpl: $1=org label (parameterized projection),
	// $2=since fixed; %s = queryBuilder for the org filter. Splitting the
	// projection from the filter lets the planner use the org index when an
	// org is provided.
	selectRepoMetricHistoryAggTpl = `SELECT COALESCE($1, '') AS org, '' AS repo, date::text,
			SUM(stars) AS stars, SUM(forks) AS forks
		FROM devpulse_repo_metric_history
		WHERE date >= $2
		  %s
		GROUP BY date
		ORDER BY date
	`

	backfillDays = 30
)

func (s *Store) GetRepoMetricHistory(ctx context.Context, org, repo *string, days int) ([]*data.RepoMetricHistory, error) {
	if s.db == nil {
		return nil, data.ErrDBNotInitialized
	}

	since := sinceDate(days)

	var rows *sql.Rows
	var err error
	if repo == nil {
		// Aggregate by date across all repos in `org` (or all orgs).
		// $1 = org label, $2 = since; queryBuilder starts at $3.
		qb := newQueryBuilder(3)
		qb.addOptional("org", org)
		query := fmt.Sprintf(selectRepoMetricHistoryAggTpl, qb.whereClause())

		args := make([]any, 0, 2+len(qb.args))
		args = append(args, org, since)
		args = append(args, qb.args...)
		rows, err = s.db.QueryContext(ctx, query, args...)
	} else {
		// Per-repo. $1 = since fixed; queryBuilder starts at $2.
		qb := newQueryBuilder(2)
		qb.addOptional("org", org)
		qb.addOptional("repo", repo)
		query := fmt.Sprintf(selectRepoMetricHistoryTpl, qb.whereClause())

		args := make([]any, 0, 1+len(qb.args))
		args = append(args, since)
		args = append(args, qb.args...)
		rows, err = s.db.QueryContext(ctx, query, args...)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query repo metric history: %w", err)
	}
	defer rows.Close()

	list := make([]*data.RepoMetricHistory, 0)
	for rows.Next() {
		m := &data.RepoMetricHistory{}
		if err := rows.Scan(&m.Org, &m.Repo, &m.Date, &m.Stars, &m.Forks); err != nil {
			return nil, fmt.Errorf("failed to scan repo metric history row: %w", err)
		}
		list = append(list, m)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating rows: %w", err)
	}

	return list, nil
}

func (s *Store) ImportRepoMetricHistory(ctx context.Context, token, owner, repo string) error {
	if s.db == nil {
		return data.ErrDBNotInitialized
	}

	client := github.NewClient(net.GetOAuthClient(ctx, token))

	r, resp, err := client.Repositories.Get(ctx, owner, repo)
	if err != nil {
		return fmt.Errorf("error getting repo %s/%s: %w", owner, repo, err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("error getting repo %s/%s: status %d", owner, repo, resp.StatusCode)
	}
	if rlErr := ghutil.CheckRateLimit(ctx, resp); rlErr != nil {
		return rlErr
	}

	currentStars := r.GetStargazersCount()
	currentForks := r.GetForksCount()
	cutoff := time.Now().AddDate(0, 0, -backfillDays).UTC()

	starsByDay, err := countRecentStarsByDay(ctx, client, owner, repo, cutoff)
	if err != nil {
		slog.Warn("counting stars by day, using flat history", "org", owner, "repo", repo, "error", err)
		starsByDay = make(map[string]int)
	}

	forksByDay, err := countRecentForksByDay(ctx, client, owner, repo, cutoff)
	if err != nil {
		slog.Warn("counting forks by day, using flat history", "org", owner, "repo", repo, "error", err)
		forksByDay = make(map[string]int)
	}

	history := buildDailyTotals(currentStars, currentForks, starsByDay, forksByDay, backfillDays)

	return s.upsertMetricHistory(ctx, owner, repo, history)
}

func countRecentStarsByDay(ctx context.Context, client *github.Client, owner, repo string, cutoff time.Time) (map[string]int, error) {
	counts := make(map[string]int)

	_, resp, err := client.Activity.ListStargazers(ctx, owner, repo, &github.ListOptions{PerPage: 100, Page: 1})
	if err != nil {
		return nil, fmt.Errorf("error listing stargazers: %w", err)
	}
	if err := ghutil.CheckRateLimit(ctx, resp); err != nil {
		return nil, err
	}

	lastPage := resp.LastPage
	if lastPage == 0 {
		lastPage = 1
	}

	for page := lastPage; page >= 1; page-- {
		stargazers, resp, err := client.Activity.ListStargazers(ctx, owner, repo, &github.ListOptions{PerPage: 100, Page: page})
		if err != nil {
			return nil, fmt.Errorf("error listing stargazers page %d: %w", page, err)
		}
		if err := ghutil.CheckRateLimit(ctx, resp); err != nil {
			return nil, err
		}

		if len(stargazers) == 0 {
			break
		}

		allOlder := true
		for _, sg := range stargazers {
			if sg.StarredAt == nil {
				continue
			}
			t := sg.StarredAt.Time
			if t.Before(cutoff) {
				continue
			}
			allOlder = false
			day := t.Format("2006-01-02")
			counts[day]++
		}

		if allOlder {
			break
		}
	}

	return counts, nil
}

func countRecentForksByDay(ctx context.Context, client *github.Client, owner, repo string, cutoff time.Time) (map[string]int, error) {
	counts := make(map[string]int)
	opt := &github.RepositoryListForksOptions{
		Sort:        "newest",
		ListOptions: github.ListOptions{PerPage: 100, Page: 1},
	}

	for {
		forks, resp, err := client.Repositories.ListForks(ctx, owner, repo, opt)
		if err != nil {
			return nil, fmt.Errorf("error listing forks: %w", err)
		}
		if err := ghutil.CheckRateLimit(ctx, resp); err != nil {
			return nil, err
		}

		if len(forks) == 0 {
			break
		}

		allOlder := true
		for _, f := range forks {
			t := f.GetCreatedAt().Time
			if t.Before(cutoff) {
				continue
			}
			allOlder = false
			day := t.Format("2006-01-02")
			counts[day]++
		}

		if allOlder {
			break
		}

		if resp.NextPage == 0 {
			break
		}
		opt.ListOptions.Page = resp.NextPage
	}

	return counts, nil
}

func buildDailyTotals(currentStars, currentForks int, starsByDay, forksByDay map[string]int, days int) []*data.RepoMetricHistory {
	now := time.Now().UTC()
	dates := make([]string, days+1)
	for i := 0; i <= days; i++ {
		dates[days-i] = now.AddDate(0, 0, -i).Format("2006-01-02")
	}

	result := make([]*data.RepoMetricHistory, len(dates))
	stars := currentStars
	forks := currentForks

	for i := len(dates) - 1; i >= 0; i-- {
		result[i] = &data.RepoMetricHistory{
			Date:  dates[i],
			Stars: stars,
			Forks: forks,
		}
		stars -= starsByDay[dates[i]]
		forks -= forksByDay[dates[i]]
		if stars < 0 {
			stars = 0
		}
		if forks < 0 {
			forks = 0
		}
	}

	return result
}

func (s *Store) upsertMetricHistory(ctx context.Context, owner, repo string, history []*data.RepoMetricHistory) error {
	if s.db == nil {
		return data.ErrDBNotInitialized
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer rollbackTransaction(tx)

	stmt, err := tx.PrepareContext(ctx, upsertRepoMetricHistorySQL)
	if err != nil {
		rollbackTransaction(tx)
		return fmt.Errorf("failed to prepare metric history statement: %w", err)
	}
	defer stmt.Close()

	slices.SortFunc(history, func(a, b *data.RepoMetricHistory) int {
		return strings.Compare(a.Date, b.Date)
	})

	for _, h := range history {
		if _, err := stmt.ExecContext(ctx, owner, repo, h.Date, h.Stars, h.Forks, h.Stars, h.Forks); err != nil {
			rollbackTransaction(tx)
			return fmt.Errorf("failed to upsert metric history %s: %w", h.Date, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit metric history: %w", err)
	}

	slog.Debug("metric history done", "org", owner, "repo", repo, "days", len(history))
	return nil
}

func (s *Store) ImportAllRepoMetricHistory(ctx context.Context, token string) error {
	if s.db == nil {
		return data.ErrDBNotInitialized
	}

	list, err := s.GetAllOrgRepos(ctx)
	if err != nil {
		return fmt.Errorf("error getting org/repo list: %w", err)
	}

	for _, r := range list {
		if err := s.ImportRepoMetricHistory(ctx, token, r.Org, r.Repo); err != nil {
			slog.Error("metric history failed", "org", r.Org, "repo", r.Repo, "error", err)
		}
	}

	return nil
}
