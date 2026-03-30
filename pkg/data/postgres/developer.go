package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"

	"github.com/thingzio/devpulse/pkg/data"
)

const (
	// insertDeveloperSQL: 12 params
	// VALUES: $1=username, $2=full_name, $3=email, $4=avatar, $5=url, $6=entity
	// ON CONFLICT UPDATE: $7=full_name, $8=email, $9=avatar, $10=url, $11=entity(case check), $12=entity(else)
	insertDeveloperSQL = `INSERT INTO developer (
			username,
			full_name,
			email,
			avatar,
			url,
			entity
		)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT(username) DO UPDATE SET
			full_name = $7,
			email = $8,
			avatar = $9,
			url = $10,
			entity = CASE WHEN $11 = '' THEN COALESCE(developer.entity, '') ELSE $12 END
	`

	selectDeveloperSQL = `SELECT
			username,
			full_name,
			email,
			avatar,
			url,
			entity
		FROM developer
		WHERE username = $1
	`

	selectDeveloperUsernameSQL = `SELECT DISTINCT username FROM developer`

	selectNoFullNameDeveloperUsernameSQL = `SELECT DISTINCT username
		FROM developer
		WHERE full_name IS NULL
		OR full_name = ''
	`

	queryDeveloperSQL = `SELECT
			username,
			COALESCE(entity, '') AS entity
		FROM developer
		WHERE username ILIKE $1
		OR email ILIKE $2
		OR entity ILIKE $3
		LIMIT $4
	`

	updateDeveloperNamesSQL = `UPDATE developer SET full_name = $1 WHERE username = $2`
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

	rows, err := stmt.Query()
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

	userStmt, err := s.db.PrepareContext(ctx, insertDeveloperSQL)
	if err != nil {
		return fmt.Errorf("failed to prepare developer insert statement: %w", err)
	}
	defer userStmt.Close()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}

	txStmt := tx.Stmt(userStmt)
	for i, u := range devs {
		if _, err = txStmt.Exec(u.Username,
			u.FullName, u.Email, u.AvatarURL, u.ProfileURL, u.Entity,
			u.FullName, u.Email, u.AvatarURL, u.ProfileURL, u.Entity, u.Entity); err != nil {
			slog.Error("failed to insert developer",
				"index", i,
				"error", err,
				"user", u.Username,
				"name", u.FullName,
				"email", u.Email,
				"avatar", u.AvatarURL,
				"profile", u.ProfileURL,
				"entity", u.Entity,
			)
			rollbackTransaction(tx)
			return fmt.Errorf("error inserting developer[%d]: %s: %w", i, u.Username, err)
		}
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
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

	row := stmt.QueryRow(username)

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
	rows, err := stmt.Query(val, val, val, limit)
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

	txStmt2 := tx.Stmt(updateStmt)
	for username, name := range devs {
		if _, err = txStmt2.Exec(name, username); err != nil {
			rollbackTransaction(tx)
			return fmt.Errorf("error updating full name for %s to %s: %w", username, name, err)
		}
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}
