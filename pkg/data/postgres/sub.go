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
	"fmt"

	"github.com/thingzio/devpulse/pkg/data"
)

const (
	insertSubSQL = `INSERT INTO devpulse_sub (type, old, new) VALUES ($1, $2, $3)
		ON CONFLICT(type, old) DO UPDATE SET new = $4
	`

	selectSubSQL = `SELECT type, old, new FROM devpulse_sub`

	updateDeveloperEntityBatchSQL = `UPDATE devpulse_developer SET entity = $1 WHERE entity = $2`
)

// developerSubSQL maps a substitution property to its constant SQL. Returning
// a literal string (not built via fmt.Sprintf) makes SQL injection impossible
// at this layer regardless of upstream whitelist mistakes.
func developerSubSQL(prop string) (string, bool) {
	switch prop {
	case "entity":
		return updateDeveloperEntityBatchSQL, true
	default:
		return "", false
	}
}

func (s *Store) applyDeveloperSub(ctx context.Context, sub *data.Substitution) error {
	if s.db == nil {
		return data.ErrDBNotInitialized
	}

	if sub == nil {
		return nil
	}

	query, ok := developerSubSQL(sub.Prop)
	if !ok {
		return fmt.Errorf("invalid property: %s (permitted options: %v)", sub.Prop, data.UpdatableProperties)
	}

	stmt, err := s.db.PrepareContext(ctx, query)
	if err != nil {
		return fmt.Errorf("failed to prepare sql statement: %w", err)
	}
	defer stmt.Close()

	res, err := stmt.ExecContext(ctx, sub.New, sub.Old)
	if err != nil {
		return fmt.Errorf("failed to execute developer property update statement: %w", err)
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	sub.Records = rows

	return nil
}

func (s *Store) SaveAndApplyDeveloperSub(ctx context.Context, prop, old, new string) (*data.Substitution, error) {
	if s.db == nil {
		return nil, data.ErrDBNotInitialized
	}

	sub := &data.Substitution{
		Prop: prop,
		Old:  old,
		New:  new,
	}

	if err := s.applyDeveloperSub(ctx, sub); err != nil {
		return nil, fmt.Errorf("failed to apply developer sub: %w", err)
	}

	subStmt, err := s.db.PrepareContext(ctx, insertSubSQL)
	if err != nil {
		return nil, fmt.Errorf("failed to prepare state insert statement: %w", err)
	}
	defer subStmt.Close()

	if _, err = subStmt.ExecContext(ctx, prop, old, new, new); err != nil {
		return nil, fmt.Errorf("failed to insert state: %w", err)
	}

	return sub, nil
}

func (s *Store) ApplySubstitutions(ctx context.Context) ([]*data.Substitution, error) {
	if s.db == nil {
		return nil, data.ErrDBNotInitialized
	}

	stmt, err := s.db.PrepareContext(ctx, selectSubSQL)
	if err != nil {
		return nil, fmt.Errorf("failed to prepare sql statement: %w", err)
	}
	defer stmt.Close()

	rows, err := stmt.QueryContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to execute substitute select statement: %w", err)
	}
	defer rows.Close()

	list := make([]*data.Substitution, 0)
	for rows.Next() {
		sub := &data.Substitution{}
		if err := rows.Scan(&sub.Prop, &sub.Old, &sub.New); err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}
		list = append(list, sub)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating rows: %w", err)
	}

	for _, sub := range list {
		if err := s.applyDeveloperSub(ctx, sub); err != nil {
			return nil, fmt.Errorf("failed to apply developer sub: %w", err)
		}
	}

	return list, nil
}
