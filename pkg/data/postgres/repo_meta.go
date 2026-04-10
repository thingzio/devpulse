package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/go-github/v83/github"
	"github.com/thingzio/devpulse/pkg/data"
	"github.com/thingzio/devpulse/pkg/data/ghutil"
	"github.com/thingzio/devpulse/pkg/net"
)

const (
	upsertRepoMetaSQL = `INSERT INTO repo_meta (org, repo, stars, forks, open_issues,
		language, license, archived,
		has_coc, has_contributing, has_readme, has_issue_template, has_pr_template, community_health_pct,
		updated_at, last_import_at, pushed_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)
		ON CONFLICT(org, repo) DO UPDATE SET
			stars = $18, forks = $19, open_issues = $20, language = $21, license = $22, archived = $23,
			has_coc = $24, has_contributing = $25, has_readme = $26, has_issue_template = $27, has_pr_template = $28, community_health_pct = $29,
			updated_at = $30, last_import_at = $31, pushed_at = $32
	`

	updateLastImportAtSQL = `UPDATE repo_meta SET last_import_at = $1 WHERE org = $2 AND repo = $3`

	// selectRepoMetaUpdatedAtSQL: $1=org, $2=repo
	selectRepoMetaUpdatedAtSQL = `SELECT COALESCE(updated_at, ''), COALESCE(community_health_pct, 0), COALESCE(pushed_at, '')
		FROM repo_meta
		WHERE org = $1 AND repo = $2
	`

	// selectRepoMetaSQL: $1=org, $2=repo
	selectRepoMetaSQL = `SELECT org, repo, stars, forks, open_issues, language, license, archived,
			has_coc, has_contributing, has_readme, has_issue_template, has_pr_template, community_health_pct,
			updated_at
		FROM repo_meta
		WHERE org = COALESCE($1, org)
		  AND repo = COALESCE($2, repo)
		ORDER BY org, repo
	`

	// selectRepoOverviewSQL: $1=since, $2=org
	// Contributors and Scored exclude fork events and bot accounts so the
	// counts reflect real code/review/issue activity only.
	selectRepoOverviewSQL = `SELECT
			rm.org, rm.repo, rm.stars, rm.forks, rm.open_issues,
			COUNT(e.type),
			COUNT(DISTINCT CASE WHEN ` + data.ContribExcludeSQL + ` THEN e.username END),
			COUNT(DISTINCT CASE WHEN ` + data.ContribExcludeSQL + ` AND d.reputation IS NOT NULL THEN e.username END),
			rm.language, rm.license, rm.archived,
			rm.last_import_at
		FROM repo_meta rm
		LEFT JOIN event e ON rm.org = e.org AND rm.repo = e.repo AND e.date >= $1
		LEFT JOIN developer d ON e.username = d.username
		WHERE rm.org = COALESCE($2, rm.org)
		GROUP BY rm.org, rm.repo, rm.stars, rm.forks, rm.open_issues,
			rm.language, rm.license, rm.archived, rm.last_import_at
		ORDER BY rm.org, rm.repo
	`
)

func (s *Store) ImportRepoMeta(ctx context.Context, token, owner, repo string) (time.Time, error) {
	var lastUpdated string
	var healthPct int
	var pushedAtStr string
	if scanErr := s.db.QueryRowContext(ctx, selectRepoMetaUpdatedAtSQL, owner, repo).Scan(&lastUpdated, &healthPct, &pushedAtStr); scanErr != nil && !errors.Is(scanErr, sql.ErrNoRows) {
		return time.Time{}, fmt.Errorf("querying repo meta updated_at for %s/%s: %w", owner, repo, scanErr)
	}
	if lastUpdated != "" && healthPct > 0 {
		if t, parseErr := time.Parse("2006-01-02T15:04:05Z", lastUpdated); parseErr == nil {
			if time.Since(t) < 24*time.Hour {
				slog.Debug("metadata fresh, skipping", "org", owner, "repo", repo, "updated_at", lastUpdated)
				now := time.Now().UTC().Format("2006-01-02T15:04:05Z")
				if _, err := s.db.ExecContext(ctx, updateLastImportAtSQL, now, owner, repo); err != nil {
					slog.Warn("failed to update last import time", "owner", owner, "repo", repo, "error", err)
				}
				pushedAt, _ := time.Parse("2006-01-02T15:04:05Z", pushedAtStr)
				return pushedAt, nil
			}
		}
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

	pushedAtVal := ""
	if r.PushedAt != nil {
		pushedAtVal = r.PushedAt.Time.UTC().Format("2006-01-02T15:04:05Z")
	}

	_, err = s.db.ExecContext(ctx, upsertRepoMetaSQL,
		owner, repo, r.GetStargazersCount(), r.GetForksCount(), r.GetOpenIssuesCount(),
		lang, license, archived, cp.coc, cp.contributing, cp.readme, cp.issueTmpl, cp.prTmpl, cp.healthPct, now, now, pushedAtVal,
		r.GetStargazersCount(), r.GetForksCount(), r.GetOpenIssuesCount(),
		lang, license, archived, cp.coc, cp.contributing, cp.readme, cp.issueTmpl, cp.prTmpl, cp.healthPct, now, now, pushedAtVal,
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
	pushedAt, _ := time.Parse("2006-01-02T15:04:05Z", pushedAtVal)
	return pushedAt, nil
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

	rows, err := s.db.QueryContext(ctx, selectRepoMetaSQL, org, repo)
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

	rows, err := s.db.QueryContext(ctx, selectRepoOverviewSQL, since, org)
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
