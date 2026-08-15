package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/go-github/v83/github"
	"github.com/thingzio/devpulse/pkg/data"
	"github.com/thingzio/devpulse/pkg/data/ghutil"
	"github.com/thingzio/devpulse/pkg/net"
)

const (
	upsertRepoMetaSQL = `INSERT INTO devpulse_repo_meta (org, repo, stars, forks, open_issues,
		language, license, archived,
		has_coc, has_contributing, has_readme, has_issue_template, has_pr_template, community_health_pct,
		updated_at, last_import_at, pushed_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)
		ON CONFLICT(org, repo) DO UPDATE SET
			stars = $18, forks = $19, open_issues = $20, language = $21, license = $22, archived = $23,
			has_coc = $24, has_contributing = $25, has_readme = $26, has_issue_template = $27, has_pr_template = $28, community_health_pct = $29,
			updated_at = $30, last_import_at = $31, pushed_at = $32
	`

	updateLastImportAtSQL = `UPDATE devpulse_repo_meta SET last_import_at = $1 WHERE org = $2 AND repo = $3`

	// selectRepoMetaUpdatedAtSQL: $1=org, $2=repo. updated_at/pushed_at are
	// scanned as timestamps (not rendered strings) because callers compare
	// them rather than serialize them.
	selectRepoMetaUpdatedAtSQL = `SELECT updated_at, COALESCE(community_health_pct, 0), pushed_at
		FROM devpulse_repo_meta
		WHERE org = $1 AND repo = $2
	`

	// selectRepoMetaTpl: %s = queryBuilder whereClause for org/repo.
	// Replaces COALESCE($N, col) anti-pattern so the planner can use the
	// (org, repo) primary key when both are provided.
	selectRepoMetaTpl = `SELECT org, repo, stars, forks, open_issues, language, license, archived,
			has_coc, has_contributing, has_readme, has_issue_template, has_pr_template, community_health_pct,
			COALESCE(TO_CHAR(updated_at AT TIME ZONE 'UTC', ` + tsLayout + `), '')
		FROM devpulse_repo_meta
		WHERE 1=1
		  %s
		ORDER BY org, repo
	`

	// selectRepoOverviewTpl: $1=since fixed; /*WHERE*/ marker is replaced
	// via strings.Replace with queryBuilder whereClause for rm.org.
	// fmt.Sprintf can't be used here because data.ContribExcludeSQL
	// contains '%[bot]' which is mis-parsed as a format directive.
	// Contributors and Scored exclude fork events and bot accounts so
	// the counts reflect real code/review/issue activity only.
	selectRepoOverviewTpl = `SELECT
			rm.org, rm.repo, rm.stars, rm.forks, rm.open_issues,
			COUNT(e.type),
			COUNT(DISTINCT CASE WHEN ` + data.ContribExcludeSQL + ` THEN e.username END),
			COUNT(DISTINCT CASE WHEN ` + data.ContribExcludeSQL + ` AND d.reputation IS NOT NULL THEN e.username END),
			rm.language, rm.license, rm.archived,
			COALESCE(TO_CHAR(rm.last_import_at AT TIME ZONE 'UTC', ` + tsLayout + `), '')
		FROM devpulse_repo_meta rm
		LEFT JOIN devpulse_event e ON rm.org = e.org AND rm.repo = e.repo AND e.date >= $1
		LEFT JOIN devpulse_developer d ON e.username = d.username
		WHERE 1=1
		  /*WHERE*/
		GROUP BY rm.org, rm.repo, rm.stars, rm.forks, rm.open_issues,
			rm.language, rm.license, rm.archived, rm.last_import_at
		ORDER BY rm.org, rm.repo
	`
)

func (s *Store) ImportRepoMeta(ctx context.Context, token, owner, repo string) (time.Time, error) {
	if s.db == nil {
		return time.Time{}, data.ErrDBNotInitialized
	}

	var lastUpdated sql.NullTime
	var healthPct int
	var pushedAt sql.NullTime
	if scanErr := s.db.QueryRowContext(ctx, selectRepoMetaUpdatedAtSQL, owner, repo).Scan(&lastUpdated, &healthPct, &pushedAt); scanErr != nil && !errors.Is(scanErr, sql.ErrNoRows) {
		return time.Time{}, fmt.Errorf("querying repo meta updated_at for %s/%s: %w", owner, repo, scanErr)
	}
	if lastUpdated.Valid && healthPct > 0 && time.Since(lastUpdated.Time) < 24*time.Hour {
		slog.Debug("metadata fresh, skipping", "org", owner, "repo", repo, "updated_at", lastUpdated.Time)
		now := time.Now().UTC().Format("2006-01-02T15:04:05Z")
		if _, err := s.db.ExecContext(ctx, updateLastImportAtSQL, now, owner, repo); err != nil {
			slog.Warn("failed to update last import time", "owner", owner, "repo", repo, "error", err)
		}
		return pushedAt.Time, nil
	}

	client := github.NewClient(net.GetOAuthClient(ctx, token))

	r, resp, err := client.Repositories.Get(ctx, owner, repo)
	if err != nil {
		return time.Time{}, fmt.Errorf("error getting repo %s/%s: %w", owner, repo, err)
	}
	if resp.StatusCode != http.StatusOK {
		return time.Time{}, fmt.Errorf("error getting repo %s/%s: status %d", owner, repo, resp.StatusCode)
	}
	if rlErr := ghutil.CheckRateLimit(ctx, resp); rlErr != nil {
		return time.Time{}, rlErr
	}

	cp, rlErr := fetchCommunityProfile(ctx, client, owner, repo)
	if rlErr != nil {
		return time.Time{}, rlErr
	}

	now := time.Now().UTC().Format("2006-01-02T15:04:05Z")
	lang := r.GetLanguage()
	var license string
	if r.License != nil {
		license = r.License.GetSPDXID()
	}
	archived := 0
	if r.GetArchived() {
		archived = 1
	}

	// pushed_at is nullable: bind nil rather than "" when GitHub reports no
	// timestamp, since "" is not a valid TIMESTAMPTZ literal.
	var newPushedAt time.Time
	var pushedAtParam any
	if r.PushedAt != nil {
		newPushedAt = r.PushedAt.Time.UTC()
		pushedAtParam = newPushedAt.Format("2006-01-02T15:04:05Z")
	}

	_, err = s.db.ExecContext(ctx, upsertRepoMetaSQL,
		owner, repo, r.GetStargazersCount(), r.GetForksCount(), r.GetOpenIssuesCount(),
		lang, license, archived, cp.coc, cp.contributing, cp.readme, cp.issueTmpl, cp.prTmpl, cp.healthPct, now, now, pushedAtParam,
		r.GetStargazersCount(), r.GetForksCount(), r.GetOpenIssuesCount(),
		lang, license, archived, cp.coc, cp.contributing, cp.readme, cp.issueTmpl, cp.prTmpl, cp.healthPct, now, now, pushedAtParam,
	)
	if err != nil {
		return time.Time{}, fmt.Errorf("error upserting repo meta %s/%s: %w", owner, repo, err)
	}

	today := time.Now().UTC().Format("2006-01-02")
	_, err = s.db.ExecContext(ctx, upsertRepoMetricHistorySQL,
		owner, repo, today, r.GetStargazersCount(), r.GetForksCount(),
		r.GetStargazersCount(), r.GetForksCount(),
	)
	if err != nil {
		return time.Time{}, fmt.Errorf("error upserting repo metric history %s/%s: %w", owner, repo, err)
	}

	slog.Debug("metadata done", "org", owner, "repo", repo)
	return newPushedAt, nil
}

type communityProfile struct {
	coc, contributing, readme, issueTmpl, prTmpl, healthPct int
}

func fetchCommunityProfile(ctx context.Context, client *github.Client, owner, repo string) (communityProfile, error) {
	var cp communityProfile

	profile, resp, err := client.Repositories.GetCommunityHealthMetrics(ctx, owner, repo)
	if err != nil {
		slog.Warn("failed to get community profile", "org", owner, "repo", repo, "error", err)
		return cp, nil
	}
	if resp != nil {
		if rlErr := ghutil.CheckRateLimit(ctx, resp); rlErr != nil {
			return cp, rlErr
		}
	}

	if profile != nil {
		if profile.Files != nil {
			if profile.Files.CodeOfConduct != nil {
				cp.coc = 1
			}
			if profile.Files.Contributing != nil {
				cp.contributing = 1
			}
			if profile.Files.Readme != nil {
				cp.readme = 1
			}
			if profile.Files.IssueTemplate != nil {
				cp.issueTmpl = 1
			}
			if profile.Files.PullRequestTemplate != nil {
				cp.prTmpl = 1
			}
		}
		if profile.HealthPercentage != nil {
			cp.healthPct = *profile.HealthPercentage
		}
	}

	return cp, nil
}

func (s *Store) ImportAllRepoMeta(ctx context.Context, token string) error {
	if s.db == nil {
		return data.ErrDBNotInitialized
	}

	list, err := s.GetAllOrgRepos(ctx)
	if err != nil {
		return fmt.Errorf("error getting org/repo list: %w", err)
	}

	for _, r := range list {
		if _, err := s.ImportRepoMeta(ctx, token, r.Org, r.Repo); err != nil {
			slog.Error("metadata failed", "org", r.Org, "repo", r.Repo, "error", err)
		}
	}

	return nil
}

func (s *Store) GetRepoMetas(ctx context.Context, org, repo *string) ([]*data.RepoMeta, error) {
	if s.db == nil {
		return nil, data.ErrDBNotInitialized
	}

	qb := newQueryBuilder(1)
	qb.addOptional("org", org)
	qb.addOptional("repo", repo)
	query := fmt.Sprintf(selectRepoMetaTpl, qb.whereClause())

	rows, err := s.db.QueryContext(ctx, query, qb.args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query repo meta: %w", err)
	}
	defer rows.Close()

	list := make([]*data.RepoMeta, 0)
	for rows.Next() {
		m := &data.RepoMeta{}
		var archived, hasCoc, hasContrib, hasReadme, hasIssueTmpl, hasPRTmpl int
		if err := rows.Scan(&m.Org, &m.Repo, &m.Stars, &m.Forks, &m.OpenIssues,
			&m.Language, &m.License, &archived,
			&hasCoc, &hasContrib, &hasReadme, &hasIssueTmpl, &hasPRTmpl, &m.CommunityHealthPct,
			&m.UpdatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan repo meta row: %w", err)
		}
		m.Archived = archived != 0
		m.HasCoC = hasCoc != 0
		m.HasContributing = hasContrib != 0
		m.HasReadme = hasReadme != 0
		m.HasIssueTemplate = hasIssueTmpl != 0
		m.HasPRTemplate = hasPRTmpl != 0
		list = append(list, m)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating rows: %w", err)
	}

	return list, nil
}

func (s *Store) GetRepoOverview(ctx context.Context, org *string, days int) ([]*data.RepoOverview, error) {
	if s.db == nil {
		return nil, data.ErrDBNotInitialized
	}

	since := sinceDate(days)

	// $1 = since fixed; queryBuilder starts at $2 for rm.org filter.
	// strings.Replace (not fmt.Sprintf) — template embeds ContribExcludeSQL
	// which contains '%[bot]' that fmt would mis-parse.
	qb := newQueryBuilder(2)
	qb.addOptional("rm.org", org)
	query := strings.Replace(selectRepoOverviewTpl, "/*WHERE*/", qb.whereClause(), 1)

	args := make([]any, 0, 1+len(qb.args))
	args = append(args, since)
	args = append(args, qb.args...)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query repo overview: %w", err)
	}
	defer rows.Close()

	list := make([]*data.RepoOverview, 0)
	for rows.Next() {
		r := &data.RepoOverview{}
		var archived int
		if err := rows.Scan(&r.Org, &r.Repo, &r.Stars, &r.Forks, &r.OpenIssues,
			&r.Events, &r.Contributors, &r.Scored,
			&r.Language, &r.License, &archived, &r.LastImport); err != nil {
			return nil, fmt.Errorf("failed to scan repo overview row: %w", err)
		}
		r.Archived = archived != 0
		list = append(list, r)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating repo overview rows: %w", err)
	}

	return list, nil
}
