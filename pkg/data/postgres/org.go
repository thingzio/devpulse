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
	"errors"
	"fmt"

	"github.com/thingzio/devpulse/pkg/data"
)

const (
	// selectOrgEntityPercentTpl: $1=since, dynamic entity/org/repo + exclusions via queryBuilder
	// %s = queryBuilder whereClause (includes optional filters + exclusion NOT IN)
	selectOrgEntityPercentTpl = `SELECT
			entity,
			ROUND(100.0 * events / (SUM(events) OVER ())) AS percent
		FROM (
			SELECT
				d.entity,
				COUNT(*) as events
			FROM devpulse_developer d
			JOIN devpulse_event e ON d.username = e.username
			WHERE d.entity IS NOT NULL AND d.entity <> ''
			AND e.date >= $1
			` + forkExcludeSQL + `
			%s
			GROUP BY d.entity
		) dt
		ORDER BY 2 DESC
	`

	// selectDeveloperPercentTpl: $1=since, dynamic entity/org/repo + exclusions via queryBuilder
	// %s = queryBuilder whereClause (includes optional filters + exclusion NOT IN)
	selectDeveloperPercentTpl = `SELECT
			username,
			ROUND(100.0 * events / (SUM(events) OVER ())) AS percent
		FROM (
			SELECT
				COALESCE(NULLIF(d.username, ''), 'unknown') AS username,
				COUNT(*) as events
			FROM devpulse_developer d
			JOIN devpulse_event e ON d.username = e.username
			WHERE e.date >= $1
			` + botExcludeTpl + `
			` + forkExcludeSQL + `
			%s
			GROUP BY d.username
		) dt
		ORDER BY 2 DESC
	`

	selectOrgLikeSQL = `SELECT org, COUNT(DISTINCT repo) as repo_count, COUNT(*) as event_count
		FROM devpulse_event
		WHERE org ILIKE $1
		GROUP BY org
		ORDER BY org DESC
		LIMIT $2
	`

	selectAllOrgReposSQL = `SELECT org, repo FROM devpulse_repo_meta ORDER BY org, repo`

	// selectDeveloperSearchTpl: $1=pattern, $2=since, $3=limit, %s=queryBuilder whereClause
	selectDeveloperSearchTpl = `SELECT DISTINCT d.username
		FROM devpulse_developer d
		JOIN devpulse_event e ON d.username = e.username
		WHERE d.username ILIKE $1
		  ` + botExcludeDTpl + `
		  AND e.date >= $2
		  ` + forkExcludeSQL + `
		  %s
		ORDER BY d.username
		LIMIT $3
	`
)

func (s *Store) GetAllOrgRepos(ctx context.Context) ([]*data.OrgRepoItem, error) {
	if s.db == nil {
		return nil, data.ErrDBNotInitialized
	}

	rows, err := s.db.QueryContext(ctx, selectAllOrgReposSQL)
	if err != nil {
		return nil, fmt.Errorf("failed to query org repos: %w", err)
	}
	defer rows.Close()

	list := make([]*data.OrgRepoItem, 0)
	for rows.Next() {
		e := &data.OrgRepoItem{}
		if err := rows.Scan(&e.Org, &e.Repo); err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}
		list = append(list, e)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating rows: %w", err)
	}

	return list, nil
}

func (s *Store) getPercentages(ctx context.Context, sqlTpl, exColumn string, entity, org, repo *string, ex []string, days int) ([]*data.CountedItem, error) {
	if s.db == nil {
		return nil, data.ErrDBNotInitialized
	}

	since := sinceDate(days)

	// $1=since. Dynamic filters start at $2.
	qb := newQueryBuilder(2)
	qb.addOptional("d.entity", entity)
	qb.addOptional("e.org", org)
	qb.addOptional("e.repo", repo)

	for _, v := range ex {
		qb.clauses = append(qb.clauses, fmt.Sprintf("%s != $%d", exColumn, qb.paramIdx))
		qb.args = append(qb.args, v)
		qb.paramIdx++
	}

	formattedSQL := fmt.Sprintf(sqlTpl, qb.whereClause())
	args := append([]any{since}, qb.args...)

	rows, err := s.db.QueryContext(ctx, formattedSQL, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to execute select statement: %w", err)
	}
	defer rows.Close()

	list := make([]*data.CountedItem, 0)
	for rows.Next() {
		e := &data.CountedItem{}
		if err := rows.Scan(&e.Name, &e.Count); err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}
		list = append(list, e)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating rows: %w", err)
	}

	return list, nil
}

func (s *Store) GetDeveloperPercentages(ctx context.Context, entity, org, repo *string, ex []string, days int) ([]*data.CountedItem, error) {
	return s.getPercentages(ctx, selectDeveloperPercentTpl, "d.username", entity, org, repo, ex, days)
}

func (s *Store) GetEntityPercentages(ctx context.Context, entity, org, repo *string, ex []string, days int) ([]*data.CountedItem, error) {
	return s.getPercentages(ctx, selectOrgEntityPercentTpl, "d.entity", entity, org, repo, ex, days)
}

func (s *Store) SearchDeveloperUsernames(ctx context.Context, query string, org, repo *string, days, limit int) ([]string, error) {
	if s.db == nil {
		return nil, data.ErrDBNotInitialized
	}

	if query == "" {
		return nil, fmt.Errorf("query is required")
	}

	since := sinceDate(days)
	pattern := fmt.Sprintf("%%%s%%", query)

	// Fixed params: $1=pattern, $2=since, $3=limit. queryBuilder starts at $4.
	qb := newQueryBuilder(4)
	qb.addOptional("e.org", org)
	qb.addOptional("e.repo", repo)

	sqlStr := fmt.Sprintf(selectDeveloperSearchTpl, qb.whereClause())

	args := make([]any, 0, 3+len(qb.args))
	args = append(args, pattern, since, limit)
	args = append(args, qb.args...)

	rows, err := s.db.QueryContext(ctx, sqlStr, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to search developers: %w", err)
	}
	defer rows.Close()

	list := make([]string, 0)
	for rows.Next() {
		var username string
		if err := rows.Scan(&username); err != nil {
			return nil, fmt.Errorf("failed to scan developer row: %w", err)
		}
		list = append(list, username)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating rows: %w", err)
	}

	return list, nil
}

func (s *Store) GetOrgLike(ctx context.Context, query string, limit int) ([]*data.ListItem, error) {
	if s.db == nil {
		return nil, data.ErrDBNotInitialized
	}

	if query == "" {
		return nil, errors.New("query is required")
	}

	stmt, err := s.db.PrepareContext(ctx, selectOrgLikeSQL)
	if err != nil {
		return nil, fmt.Errorf("failed to prepare org like statement: %w", err)
	}
	defer stmt.Close()

	query = fmt.Sprintf("%%%s%%", query)
	rows, err := stmt.QueryContext(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to execute select statement: %w", err)
	}
	defer rows.Close()

	list := make([]*data.ListItem, 0)
	for rows.Next() {
		e := &data.ListItem{}
		var repoCount, eventCount int
		if err := rows.Scan(&e.Value, &repoCount, &eventCount); err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}
		e.Text = fmt.Sprintf("%s (%d repos, %d events)", e.Value, repoCount, eventCount)
		list = append(list, e)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating rows: %w", err)
	}

	return list, nil
}
