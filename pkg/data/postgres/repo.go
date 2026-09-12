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
	selectRepoLikeSQL = `SELECT org, repo, COUNT(*) as event_count
		FROM devpulse_event
		WHERE repo ILIKE $1
		GROUP BY org, repo
		ORDER BY org DESC, repo DESC
		LIMIT $2
	`
)

func (s *Store) GetRepoLike(ctx context.Context, query string, limit int) ([]*data.ListItem, error) {
	if s.db == nil {
		return nil, data.ErrDBNotInitialized
	}

	if query == "" {
		return nil, errors.New("query is required")
	}

	stmt, err := s.db.PrepareContext(ctx, selectRepoLikeSQL)
	if err != nil {
		return nil, fmt.Errorf("failed to prepare repo like statement: %w", err)
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
		var org, repo string
		var count int
		if err := rows.Scan(&org, &repo, &count); err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}
		e := &data.ListItem{
			Value: fmt.Sprintf("%s/%s", org, repo),
			Text:  fmt.Sprintf("%s/%s (%d events)", org, repo, count),
		}
		list = append(list, e)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating rows: %w", err)
	}

	return list, nil
}
