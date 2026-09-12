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
	"log/slog"
	"net/http"
	"strings"

	"github.com/google/go-github/v83/github"
	"github.com/thingzio/devpulse/pkg/data"
	"github.com/thingzio/devpulse/pkg/data/ghutil"
	"github.com/thingzio/devpulse/pkg/net"
)

const (
	// insertDeveloperSQL: 12 params
	// VALUES: $1=username, $2=full_name, $3=email, $4=avatar, $5=url, $6=entity
	// ON CONFLICT UPDATE: $7=full_name, $8=email, $9=avatar, $10=url, $11=entity(case check), $12=entity(else)
	// NULLIF($6,'') ensures new developers start with entity=NULL (never enriched).
	// ON CONFLICT keeps existing entity when incoming is '' (partial user from event API).
	insertDeveloperSQL = `INSERT INTO devpulse_developer (
			username,
			full_name,
			email,
			avatar,
			url,
			entity
		)
		VALUES ($1, $2, $3, $4, $5, NULLIF($6, ''))
		ON CONFLICT(username) DO UPDATE SET
			full_name = $7,
			email = $8,
			avatar = $9,
			url = $10,
			entity = CASE WHEN $11 = '' THEN devpulse_developer.entity ELSE $12 END
	`

	selectDeveloperSQL = `SELECT
			username,
			full_name,
			email,
			avatar,
			url,
			COALESCE(entity, '') AS entity
		FROM devpulse_developer
		WHERE username = $1
	`

	selectDeveloperUsernameSQL = `SELECT DISTINCT username FROM devpulse_developer`

	selectNoFullNameDeveloperUsernameSQL = `SELECT DISTINCT username
		FROM devpulse_developer
		WHERE full_name IS NULL
		OR full_name = ''
	`

	// selectUnenrichedDeveloperUsernameSQL returns developers whose entity has never
	// been populated from GitHub (NULL = never enriched). Limit prevents burning
	// too much API quota in a single execution.
	selectUnenrichedDeveloperUsernameSQL = `SELECT username FROM devpulse_developer WHERE entity IS NULL LIMIT 200`

	// updateDeveloperEntitySQL is used by enrichment — always writes entity (even '').
	// This marks developers as "enriched but no company" so they are not re-fetched.
	updateDeveloperEntitySQL = `UPDATE devpulse_developer SET entity = $2 WHERE username = $1`

	queryDeveloperSQL = `SELECT
			username,
			COALESCE(entity, '') AS entity
		FROM devpulse_developer
		WHERE username ILIKE $1
		OR email ILIKE $2
		OR entity ILIKE $3
		LIMIT $4
	`

	updateDeveloperNamesSQL = `UPDATE devpulse_developer SET full_name = $1 WHERE username = $2`
)

func (s *Store) GetDeveloperUsernames(ctx context.Context) ([]string, error) {
	return s.getDBSlice(ctx, selectDeveloperUsernameSQL)
}

func (s *Store) GetNoFullnameDeveloperUsernames(ctx context.Context) ([]string, error) {
	return s.getDBSlice(ctx, selectNoFullNameDeveloperUsernameSQL)
}

func (s *Store) getDBSlice(ctx context.Context, sqlQuery string) ([]string, error) {
	if s.db == nil {
		return nil, data.ErrDBNotInitialized
	}

	stmt, err := s.db.PrepareContext(ctx, sqlQuery)
	if err != nil {
		return nil, fmt.Errorf("failed to prepare sql statement: %w", err)
	}
	defer stmt.Close()

	list := make([]string, 0)

	rows, err := stmt.QueryContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to execute series select statement: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var u string
		if err := rows.Scan(&u); err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}
		list = append(list, u)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating rows: %w", err)
	}

	return list, nil
}

func (s *Store) SaveDevelopers(ctx context.Context, devs []*data.Developer) error {
	if s.db == nil {
		return data.ErrDBNotInitialized
	}

	if len(devs) == 0 {
		return nil
	}

	// Build multi-row INSERT ... ON CONFLICT DO UPDATE.
	// Single statement = single implicit transaction = no deadlock possible.
	var b strings.Builder
	b.WriteString(`INSERT INTO devpulse_developer (username, full_name, email, avatar, url, entity) VALUES `)

	args := make([]any, 0, len(devs)*6)
	for i, d := range devs {
		if i > 0 {
			b.WriteString(", ")
		}
		base := i * 6
		fmt.Fprintf(&b, "($%d, $%d, $%d, $%d, $%d, NULLIF($%d, ''))",
			base+1, base+2, base+3, base+4, base+5, base+6)
		args = append(args, d.Username, d.FullName, d.Email, d.AvatarURL, d.ProfileURL, d.Entity)
	}

	b.WriteString(` ON CONFLICT(username) DO UPDATE SET
		full_name = EXCLUDED.full_name,
		email = EXCLUDED.email,
		avatar = EXCLUDED.avatar,
		url = EXCLUDED.url,
		entity = CASE WHEN EXCLUDED.entity IS NULL THEN devpulse_developer.entity ELSE EXCLUDED.entity END`)

	if _, err := s.db.ExecContext(ctx, b.String(), args...); err != nil {
		return fmt.Errorf("batch saving %d developers: %w", len(devs), err)
	}
	return nil
}

func (s *Store) GetDeveloper(ctx context.Context, username string) (*data.Developer, error) {
	if s.db == nil {
		return nil, data.ErrDBNotInitialized
	}

	stmt, err := s.db.PrepareContext(ctx, selectDeveloperSQL)
	if err != nil {
		return nil, fmt.Errorf("failed to prepare developer select statement: %w", err)
	}
	defer stmt.Close()

	row := stmt.QueryRowContext(ctx, username)

	u := &data.Developer{}
	if err = row.Scan(&u.Username, &u.FullName, &u.Email, &u.AvatarURL, &u.ProfileURL, &u.Entity); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to scan row: %w", err)
	}

	return u, nil
}

func (s *Store) SearchDevelopers(ctx context.Context, val string, limit int) ([]*data.DeveloperListItem, error) {
	if s.db == nil {
		return nil, data.ErrDBNotInitialized
	}

	stmt, err := s.db.PrepareContext(ctx, queryDeveloperSQL)
	if err != nil {
		return nil, fmt.Errorf("failed to prepare developer query statement: %w", err)
	}
	defer stmt.Close()

	val = fmt.Sprintf("%%%s%%", val)
	rows, err := stmt.QueryContext(ctx, val, val, val, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to execute select statement: %w", err)
	}
	defer rows.Close()

	return mapDeveloperListItem(rows)
}

func (s *Store) UpdateDeveloperNames(ctx context.Context, devs map[string]string) error {
	if s.db == nil {
		return data.ErrDBNotInitialized
	}

	updateStmt, err := s.db.PrepareContext(ctx, updateDeveloperNamesSQL)
	if err != nil {
		return fmt.Errorf("failed to prepare entity update statement: %w", err)
	}
	defer updateStmt.Close()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer rollbackTransaction(tx)

	txStmt2 := tx.Stmt(updateStmt)
	defer txStmt2.Close()
	for username, name := range devs {
		if _, err = txStmt2.ExecContext(ctx, name, username); err != nil {
			rollbackTransaction(tx)
			return fmt.Errorf("error updating full name for %s to %s: %w", username, name, err)
		}
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

func (s *Store) GetUnenrichedDeveloperUsernames(ctx context.Context) ([]string, error) {
	return s.getDBSlice(ctx, selectUnenrichedDeveloperUsernameSQL)
}

// developerEnrichmentChunk is the number of (username, entity) updates committed
// per transaction. Sized so progress is not lost on transient failure but each
// transaction stays well under typical lock-wait timeouts.
const developerEnrichmentChunk = 50

// EnrichDeveloperEntities fetches full GitHub profiles for developers whose entity
// is NULL (never enriched) and writes entity back. Developers with no GitHub company
// get entity=” which marks them as enriched so they are not re-fetched next run.
//
// Updates are batched into transactions of developerEnrichmentChunk rows so the
// per-row round-trip overhead is amortized and a transient failure does not
// invalidate progress made by earlier chunks.
func (s *Store) EnrichDeveloperEntities(ctx context.Context, token string) error {
	if s.db == nil {
		return data.ErrDBNotInitialized
	}

	usernames, err := s.GetUnenrichedDeveloperUsernames(ctx)
	if err != nil {
		return fmt.Errorf("getting unenriched developer usernames: %w", err)
	}

	if len(usernames) == 0 {
		return nil
	}

	slog.Info("enriching developer profiles", "count", len(usernames))

	client := net.GetOAuthClient(ctx, token)

	type pendingUpdate struct {
		username string
		entity   string
	}
	pending := make([]pendingUpdate, 0, developerEnrichmentChunk)

	flush := func() error {
		if len(pending) == 0 {
			return nil
		}
		tx, txErr := s.db.BeginTx(ctx, nil)
		if txErr != nil {
			return fmt.Errorf("beginning enrichment tx: %w", txErr)
		}
		defer rollbackTransaction(tx)

		stmt, prepErr := tx.PrepareContext(ctx, updateDeveloperEntitySQL)
		if prepErr != nil {
			return fmt.Errorf("preparing developer entity update: %w", prepErr)
		}
		defer stmt.Close()

		for _, p := range pending {
			if _, execErr := stmt.ExecContext(ctx, p.username, p.entity); execErr != nil {
				return fmt.Errorf("updating developer entity %q: %w", p.username, execErr)
			}
		}
		if commitErr := tx.Commit(); commitErr != nil {
			return fmt.Errorf("committing enrichment tx: %w", commitErr)
		}
		pending = pending[:0]
		return nil
	}

	var enriched, skipped int
	for _, username := range usernames {
		// Honor cancellation between API calls so worker shutdown doesn't
		// stall behind a slow GitHub response.
		if err := ctx.Err(); err != nil {
			if flushErr := flush(); flushErr != nil {
				slog.Warn("flushing enrichment on cancel", "error", flushErr)
			}
			return fmt.Errorf("developer enrichment canceled: %w", err)
		}

		dev, fetchErr := ghutil.GetGitHubDeveloper(ctx, client, username)
		if fetchErr != nil {
			// If user is deleted/renamed (404), mark as enriched with empty entity
			// so they are not re-fetched on every run.
			var ghErr *github.ErrorResponse
			if errors.As(fetchErr, &ghErr) && ghErr.Response != nil && ghErr.Response.StatusCode == http.StatusNotFound {
				slog.Debug("developer not found on github, marking enriched", "username", username)
				pending = append(pending, pendingUpdate{username: username, entity: ""})
				skipped++
			} else {
				slog.Warn("fetching developer profile", "username", username, "error", fetchErr)
				skipped++
			}
		} else {
			pending = append(pending, pendingUpdate{username: username, entity: cleanEntityName(dev.Entity)})
			enriched++
		}

		if len(pending) >= developerEnrichmentChunk {
			if flushErr := flush(); flushErr != nil {
				return flushErr
			}
		}
	}

	if flushErr := flush(); flushErr != nil {
		return flushErr
	}

	slog.Info("developer enrichment complete",
		"enriched", enriched,
		"skipped", skipped,
		"total", len(usernames))

	return nil
}
