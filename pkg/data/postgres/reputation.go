package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/mchmarny/reputer/pkg/score"
	"github.com/thingzio/devpulse/pkg/data"
)

const (
	reputationStaleHours = 24

	// selectStaleReputationUsernamesTpl: $1=threshold, %s=queryBuilder whereClause
	selectStaleReputationUsernamesTpl = `SELECT DISTINCT d.username
		FROM devpulse_developer d
		JOIN devpulse_event e ON d.username = e.username
		WHERE 1=1
		  ` + botExcludeDTpl + `
		  ` + forkExcludeSQL + `
		  AND (d.reputation IS NULL
		   OR d.reputation_updated_at IS NULL
		   OR d.reputation_updated_at < $1)
		  %s
	`

	// updateReputationSQL: $1=reputation, $2=updated_at, $3=username
	updateReputationSQL = `UPDATE devpulse_developer
		SET reputation = $1, reputation_updated_at = $2
		WHERE username = $3
	`

	selectDistinctOrgsSQL = `SELECT DISTINCT org FROM devpulse_event`

	// selectUserCommitCountSQL: $1=username, $2=since
	selectUserCommitCountSQL = `SELECT COUNT(*) FROM devpulse_event
		WHERE username = $1 AND date >= $2
	`

	// selectTotalCommitCountSQL: $1=since
	selectTotalCommitCountSQL = `SELECT COUNT(*) FROM devpulse_event
		WHERE date >= $1
	`

	// selectTotalContributorCountSQL: $1=since
	selectTotalContributorCountSQL = `SELECT COUNT(DISTINCT username) FROM devpulse_event
		WHERE date >= $1
	`

	// selectLastCommitDateSQL: $1=username
	selectLastCommitDateSQL = `SELECT MAX(date) FROM devpulse_event
		WHERE username = $1
	`

	// selectContributorCompositionSQL: $1=org, $2=repo, $3=entity, $4=since
	selectContributorCompositionSQL = `
WITH contributor_roles AS (
    SELECT e.username,
        bool_or(e.type = 'pr_review') AS is_reviewer,
        bool_or(e.type = 'pr') AS is_author,
        bool_or(e.type = 'issue_comment') AS is_commenter
    FROM devpulse_event e
    WHERE e.org = COALESCE($1, e.org)
      AND e.repo = COALESCE($2, e.repo)
      AND ($3::text IS NULL OR e.username IN (
        SELECT username FROM devpulse_developer WHERE entity = $3))
      AND e.date >= $4
      AND e.username NOT LIKE '%[bot]'
      AND e.type != 'fork'
    GROUP BY e.username
)
SELECT
    COUNT(*) FILTER (WHERE is_reviewer),
    COUNT(*) FILTER (WHERE NOT is_reviewer AND is_author),
    COUNT(*) FILTER (WHERE NOT is_reviewer AND NOT is_author AND is_commenter),
    COUNT(*)
FROM contributor_roles`
)

type globalStats struct {
	totalCommits      int64
	totalContributors int
}

func (s *Store) ImportReputation(ctx context.Context, org, repo *string) (*data.ReputationResult, error) {
	if s.db == nil {
		return nil, data.ErrDBNotInitialized
	}

	threshold := time.Now().UTC().Add(-reputationStaleHours * time.Hour).Format("2006-01-02T15:04:05Z")

	usernames, err := s.getStaleReputationUsernames(ctx, org, repo, threshold)
	if err != nil {
		return nil, fmt.Errorf("error getting stale usernames: %w", err)
	}

	if len(usernames) == 0 {
		slog.Debug("reputation up to date")
		return &data.ReputationResult{}, nil
	}

	slog.Info("scoring reputation", "users", len(usernames))

	since := sinceDate(data.EventAgeDaysDefault)

	stats, err := s.computeGlobalStats(ctx, since)
	if err != nil {
		return nil, fmt.Errorf("error computing global stats: %w", err)
	}

	now := time.Now().UTC().Format("2006-01-02T15:04:05Z")
	res := &data.ReputationResult{}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("error starting reputation tx: %w", err)
	}
	defer rollbackTransaction(tx)

	stmt, err := tx.PrepareContext(ctx, updateReputationSQL)
	if err != nil {
		rollbackTransaction(tx)
		return nil, fmt.Errorf("error preparing reputation update: %w", err)
	}
	defer stmt.Close()

	total := len(usernames)
	logEvery := total / 10
	if logEvery < 1 {
		logEvery = 1
	}

	for i, username := range usernames {
		signals := s.gatherLocalSignals(ctx, username, since, stats)
		rep := score.Compute(signals)

		if _, execErr := stmt.ExecContext(ctx, rep, now, username); execErr != nil {
			rollbackTransaction(tx)
			return nil, fmt.Errorf("error updating reputation for %s: %w", username, execErr)
		}
		res.Updated++

		if (i+1)%logEvery == 0 {
			slog.Info("reputation progress", "scored", i+1, "total", total)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("error committing reputation tx: %w", err)
	}

	slog.Info("reputation done", "updated", res.Updated)

	return res, nil
}

func (s *Store) GetContributorComposition(ctx context.Context, org, repo, entity *string, days int) (*data.ContributorComposition, error) {
	if s.db == nil {
		return nil, data.ErrDBNotInitialized
	}

	since := sinceDate(days)
	c := &data.ContributorComposition{}

	var reviewers, authors, commenters, total int
	if err := s.db.QueryRowContext(ctx, selectContributorCompositionSQL, org, repo, entity, since).Scan(
		&reviewers, &authors, &commenters, &total,
	); err != nil {
		return nil, fmt.Errorf("failed to query contributor composition: %w", err)
	}

	c.Reviewers = reviewers
	c.Authors = authors
	c.Commenters = commenters
	c.Total = total
	c.Observers = total - reviewers - authors - commenters
	if c.Observers < 0 {
		c.Observers = 0
	}

	return c, nil
}

func (s *Store) gatherLocalSignals(ctx context.Context, username, since string, stats *globalStats) score.Signals {
	var sig score.Signals

	var commits int64
	if err := s.db.QueryRowContext(ctx, selectUserCommitCountSQL, username, since).Scan(&commits); err != nil && !errors.Is(err, sql.ErrNoRows) {
		slog.Debug("error counting user commits", "username", username, "error", err)
	}
	sig.Commits = commits

	sig.TotalCommits = stats.totalCommits
	sig.TotalContributors = stats.totalContributors

	var lastDate sql.NullString
	if err := s.db.QueryRowContext(ctx, selectLastCommitDateSQL, username).Scan(&lastDate); err != nil && !errors.Is(err, sql.ErrNoRows) {
		slog.Debug("error getting last commit date", "username", username, "error", err)
	}
	if lastDate.Valid && lastDate.String != "" {
		if t, parseErr := time.Parse("2006-01-02", lastDate.String); parseErr == nil {
			sig.LastCommitDays = int64(time.Since(t).Hours() / 24)
		}
	}

	return sig
}

func (s *Store) computeGlobalStats(ctx context.Context, since string) (*globalStats, error) {
	var gs globalStats

	if err := s.db.QueryRowContext(ctx, selectTotalCommitCountSQL, since).Scan(&gs.totalCommits); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("error counting total commits: %w", err)
	}

	if err := s.db.QueryRowContext(ctx, selectTotalContributorCountSQL, since).Scan(&gs.totalContributors); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("error counting total contributors: %w", err)
	}

	return &gs, nil
}

func (s *Store) getStaleReputationUsernames(ctx context.Context, org, repo *string, threshold string) ([]string, error) {
	if s.db == nil {
		return nil, data.ErrDBNotInitialized
	}

	// $1=threshold is fixed. queryBuilder starts at $2 for org/repo.
	qb := newQueryBuilder(2)
	qb.addOptional("e.org", org)
	qb.addOptional("e.repo", repo)

	query := fmt.Sprintf(selectStaleReputationUsernamesTpl, qb.whereClause())

	args := make([]any, 0, 1+len(qb.args))
	args = append(args, threshold)
	args = append(args, qb.args...)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query stale reputation usernames: %w", err)
	}
	defer rows.Close()

	list := make([]string, 0)
	for rows.Next() {
		var username string
		if err := rows.Scan(&username); err != nil {
			return nil, fmt.Errorf("failed to scan username: %w", err)
		}
		list = append(list, username)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating rows: %w", err)
	}

	return list, nil
}

func (s *Store) getDistinctOrgs(ctx context.Context) ([]string, error) {
	if s.db == nil {
		return nil, data.ErrDBNotInitialized
	}

	rows, err := s.db.QueryContext(ctx, selectDistinctOrgsSQL)
	if err != nil {
		return nil, fmt.Errorf("failed to query distinct orgs: %w", err)
	}
	defer rows.Close()

	list := make([]string, 0)
	for rows.Next() {
		var org string
		if err := rows.Scan(&org); err != nil {
			return nil, fmt.Errorf("failed to scan org: %w", err)
		}
		list = append(list, org)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating rows: %w", err)
	}

	return list, nil
}
