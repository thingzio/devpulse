package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/thingzio/devpulse/pkg/data"
)

const (
	selectMinEventDateTpl = `SELECT COALESCE(MIN(date), '') FROM devpulse_event
		WHERE 1=1%s
	`

	// selectEventTypesSinceTpl: Postgres version with generate_series.
	// %[1]s = GroupExpr(gran, "dates.d::text"), %[2]s = queryBuilder whereClause
	selectEventTypesSinceTpl = `SELECT
			date,
			SUM(prs) as prs,
			SUM(pr_review) as pr_review,
			SUM(issues) as issues,
			SUM(issue_comments) as issue_comments,
			SUM(forks) as forks
		FROM (
			SELECT
				%[1]s as date,
				CASE WHEN e.type = $3 THEN 1 ELSE 0 END as prs,
				CASE WHEN e.type = $4 THEN 1 ELSE 0 END as pr_review,
				CASE WHEN e.type = $5 THEN 1 ELSE 0 END as issues,
				CASE WHEN e.type = $6 THEN 1 ELSE 0 END as issue_comments,
				CASE WHEN e.type = $7 THEN 1 ELSE 0 END as forks
			FROM generate_series($1::date, $2::date, '1 day'::interval) AS dates(d)
			LEFT JOIN devpulse_event e ON dates.d::date = e.date::date
			JOIN devpulse_developer d ON e.username = d.username
			%[2]s
			` + botExcludeTpl + `
		) dt
		GROUP BY date
		ORDER BY 1
	`

	// selectEventTpl is the base query for SearchEvents; WHERE clauses are
	// appended dynamically by searchQueryBuilder so the planner can use indexes.
	// %s = dynamic WHERE clauses, %d/%d = LIMIT/OFFSET param positions.
	selectEventTpl = `SELECT
			e.org,
			e.repo,
			e.date,
			e.type,
			e.url,
			e.mentions,
			e.labels,
			e.state,
			e.number,
			e.created_at,
			e.closed_at,
			e.merged_at,
			e.additions,
			e.deletions,
			e.changed_files,
			e.commits,
			d.username,
			d.email,
			d.full_name,
			d.avatar,
			d.url,
			d.entity
		FROM devpulse_event e
		JOIN devpulse_developer d ON e.username = d.username
		WHERE 1=1
		` + botExcludeTpl + `
		%s
		ORDER BY e.date DESC, e.org, e.repo
		LIMIT $%d OFFSET $%d
	`
)

func optionalLike(s *string) *string {
	if s == nil || *s == "" {
		return nil
	}
	v := fmt.Sprintf("%%%s%%", *s)
	return &v
}

func (s *Store) SearchEvents(ctx context.Context, q *data.EventSearchCriteria) ([]*data.EventDetails, error) {
	if s.db == nil {
		return nil, data.ErrDBNotInitialized
	}

	qb := newQueryBuilder(1)
	qb.addOptionalGte("e.date", q.FromDate)
	qb.addOptionalLte("e.date", q.ToDate)
	qb.addOptional("e.type", q.Type)
	qb.addOptional("e.org", q.Org)
	qb.addOptional("e.repo", q.Repo)
	qb.addOptional("e.username", q.Username)
	qb.addOptionalLike("e.mentions", optionalLike(q.Mention))
	qb.addOptionalLike("e.labels", optionalLike(q.Label))
	qb.addOptional("d.entity", q.Entity)

	limitParam := qb.nextParam()
	offsetParam := limitParam + 1
	query := fmt.Sprintf(selectEventTpl, qb.whereClause(), limitParam, offsetParam)

	offset := (q.Page - 1) * q.PageSize
	args := make([]any, 0, len(qb.args)+2)
	args = append(args, qb.args...)
	args = append(args, q.PageSize, offset)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to execute event search statement: %w", err)
	}
	defer rows.Close()

	list := make([]*data.EventDetails, 0)

	for rows.Next() {
		e := &data.EventDetails{
			Event:     &data.Event{},
			Developer: &data.Developer{},
		}
		if err := rows.Scan(&e.Event.Org, &e.Event.Repo, &e.Event.Date, &e.Event.Type, &e.Event.URL,
			&e.Event.Mentions, &e.Event.Labels,
			&e.Event.State, &e.Event.Number, &e.Event.CreatedAt, &e.Event.ClosedAt, &e.Event.MergedAt,
			&e.Event.Additions, &e.Event.Deletions, &e.Event.ChangedFiles, &e.Event.Commits,
			&e.Developer.Username, &e.Developer.Email, &e.Developer.FullName,
			&e.Developer.AvatarURL, &e.Developer.ProfileURL, &e.Developer.Entity); err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}
		list = append(list, e)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating rows: %w", err)
	}

	return list, nil
}

func (s *Store) GetMinEventDate(ctx context.Context, org, repo *string) (string, error) {
	if s.db == nil {
		return "", data.ErrDBNotInitialized
	}

	qb := newQueryBuilder(1)
	qb.addOptional("org", org)
	qb.addOptional("repo", repo)

	query := fmt.Sprintf(selectMinEventDateTpl, qb.whereClause())

	var minDate string
	if err := s.db.QueryRowContext(ctx, query, qb.args...).Scan(&minDate); err != nil {
		return "", fmt.Errorf("failed to query min event date: %w", err)
	}

	return minDate, nil
}

func (s *Store) GetEventTypeSeries(ctx context.Context, org, repo, entity *string, days int) (*data.EventTypeSeries, error) {
	if s.db == nil {
		return nil, data.ErrDBNotInitialized
	}

	gran := AutoGranularity(days)

	// Fixed params: $1=since, $2=to, $3-$7=event types. queryBuilder starts at $8.
	qb := newQueryBuilder(8)
	qb.addOptional("e.org", org)
	qb.addOptional("e.repo", repo)
	qb.addOptional("d.entity", entity)

	query := fmt.Sprintf(selectEventTypesSinceTpl, GroupExpr(gran, "dates.d::text"), qb.whereClause())

	stmt, err := s.db.PrepareContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to prepare repo events statement: %w", err)
	}
	defer stmt.Close()

	since := sinceDate(days)
	to := time.Now().UTC().Format("2006-01-02")

	args := make([]any, 0, 7+len(qb.args))
	args = append(args, since, to,
		data.EventTypePR, data.EventTypePRReview, data.EventTypeIssue, data.EventTypeIssueComment, data.EventTypeFork)
	args = append(args, qb.args...)

	rows, err := stmt.QueryContext(ctx, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to execute series select statement: %w", err)
	}
	defer rows.Close()

	series := &data.EventTypeSeries{
		Dates:         make([]string, 0, 52),
		PRs:           make([]int, 0, 52),
		PRReviews:     make([]int, 0, 52),
		Issues:        make([]int, 0, 52),
		IssueComments: make([]int, 0, 52),
		Forks:         make([]int, 0, 52),
		Total:         make([]int, 0, 52),
		Trend:         make([]float32, 0, 52),
	}

	for rows.Next() {
		var date string
		var prs, prComments, issues, issueComments, forks int
		if err := rows.Scan(&date, &prs, &prComments, &issues, &issueComments, &forks); err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}
		series.Dates = append(series.Dates, date)
		series.PRs = append(series.PRs, prs)
		series.PRReviews = append(series.PRReviews, prComments)
		series.Issues = append(series.Issues, issues)
		series.IssueComments = append(series.IssueComments, issueComments)
		series.Forks = append(series.Forks, forks)
		series.Total = append(series.Total, prs+prComments+issues+issueComments+forks)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating rows: %w", err)
	}

	window := TrendWindow(gran)
	for i := range series.Total {
		start := i - window + 1
		if start < 0 {
			start = 0
		}
		var sum float32
		for j := start; j <= i; j++ {
			sum += float32(series.Total[j])
		}
		series.Trend = append(series.Trend, sum/float32(i-start+1))
	}

	return series, nil
}
