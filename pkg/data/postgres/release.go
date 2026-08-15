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
	// insertReleaseSQL: 9 params
	insertReleaseSQL = `INSERT INTO devpulse_release (org, repo, tag, name, published_at, prerelease)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT(org, repo, tag) DO UPDATE SET
			name = $7, published_at = $8, prerelease = $9
	`

	// selectReleaseCadenceTpl: $1=since (fixed); first %s = GroupExpr(gran),
	// second %s = queryBuilder whereClause for org/repo. Replaces COALESCE
	// anti-pattern so the (org, repo) primary key can be used.
	selectReleaseCadenceTpl = `SELECT
			%s AS period,
			COUNT(*) AS total,
			SUM(CASE WHEN prerelease = 0 THEN 1 ELSE 0 END) AS stable
		FROM devpulse_release
		WHERE published_at >= $1
		  %s
		GROUP BY period
		ORDER BY period
	`

	// insertReleaseAssetSQL: 10 params
	insertReleaseAssetSQL = `INSERT INTO devpulse_release_asset (org, repo, tag, name, content_type, size, download_count)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT(org, repo, tag, name) DO UPDATE SET
			content_type = $8, size = $9, download_count = $10
	`

	// selectReleaseDownloadsTpl: $1=since (fixed); first %s = GroupExpr(gran),
	// second %s = queryBuilder whereClause for ra.org / ra.repo.
	selectReleaseDownloadsTpl = `SELECT
			%s AS period,
			SUM(ra.download_count) AS downloads
		FROM devpulse_release_asset ra
		JOIN devpulse_release r ON ra.org = r.org AND ra.repo = r.repo AND ra.tag = r.tag
		WHERE r.published_at >= $1
		  %s
		GROUP BY period
		ORDER BY period
	`

	// selectMergedPRDeploymentsTpl: $1=since (fixed); first %s = GroupExpr(gran),
	// second %s = queryBuilder whereClause for org/repo/entity.
	selectMergedPRDeploymentsTpl = `SELECT
			%s AS period,
			COUNT(*) AS cnt
		FROM devpulse_event e
		JOIN devpulse_developer d ON e.username = d.username
		WHERE e.type = 'pr'
		  AND e.state = 'merged'
		  AND e.merged_at IS NOT NULL
		  AND e.merged_at >= $1
		  ` + botExcludeTpl + `
		  %s
		GROUP BY period
		ORDER BY period
	`

	// selectLatestReleaseSQL: $1=org, $2=repo
	selectLatestReleaseSQL = `SELECT COALESCE(TO_CHAR(MAX(published_at) AT TIME ZONE 'UTC', ` + tsLayout + `), '')
		FROM devpulse_release
		WHERE org = $1 AND repo = $2
	`

	// selectReleaseDownloadsByTagTpl: $1=since (fixed); first %s = queryBuilder
	// for r.org / r.repo (recent CTE), second %s = queryBuilder for ra.org /
	// ra.repo (top CTE). Two builders so each CTE keeps its own table aliases.
	selectReleaseDownloadsByTagTpl = `WITH recent AS (
			SELECT r.org, r.repo, r.tag, r.published_at
			FROM devpulse_release r
			WHERE r.published_at >= $1
			  %s
			ORDER BY r.published_at DESC
			LIMIT 9
		), top AS (
			SELECT ra.org, ra.repo, ra.tag, r.published_at
			FROM devpulse_release_asset ra
			JOIN devpulse_release r ON ra.org = r.org AND ra.repo = r.repo AND ra.tag = r.tag
			WHERE r.published_at >= $1
			  %s
			GROUP BY ra.org, ra.repo, ra.tag, r.published_at
			ORDER BY SUM(ra.download_count) DESC
			LIMIT 1
		), combined AS (
			SELECT org, repo, tag, published_at FROM recent
			UNION
			SELECT org, repo, tag, published_at FROM top
		)
		SELECT c.tag, COALESCE(SUM(ra.download_count), 0) AS downloads
		FROM combined c
		LEFT JOIN devpulse_release_asset ra ON c.org = ra.org AND c.repo = ra.repo AND c.tag = ra.tag
		GROUP BY c.tag, c.published_at
		ORDER BY c.published_at
	`
)

func (s *Store) ImportReleases(ctx context.Context, token, owner, repo string) error {
	if s.db == nil {
		return data.ErrDBNotInitialized
	}

	client := github.NewClient(net.GetOAuthClient(ctx, token))

	var latestPublishedAt string
	if scanErr := s.db.QueryRowContext(ctx, selectLatestReleaseSQL, owner, repo).Scan(&latestPublishedAt); scanErr != nil {
		return fmt.Errorf("querying latest release for %s/%s: %w", owner, repo, scanErr)
	}

	stmt, err := s.db.PrepareContext(ctx, insertReleaseSQL)
	if err != nil {
		return fmt.Errorf("error preparing release insert: %w", err)
	}
	defer stmt.Close()

	assetStmt, err := s.db.PrepareContext(ctx, insertReleaseAssetSQL)
	if err != nil {
		return fmt.Errorf("error preparing release asset insert: %w", err)
	}
	defer assetStmt.Close()

	opt := &github.ListOptions{PerPage: pageSizeDefault, Page: 1}

	for {
		releases, resp, listErr := client.Repositories.ListReleases(ctx, owner, repo, opt)
		if listErr != nil {
			return fmt.Errorf("error listing releases %s/%s: %w", owner, repo, listErr)
		}
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("error listing releases %s/%s: status %d", owner, repo, resp.StatusCode)
		}
		if err := ghutil.CheckRateLimit(ctx, resp); err != nil {
			return fmt.Errorf("rate limit during release import: %w", err)
		}

		if len(releases) == 0 {
			break
		}

		seenOld, upsertErr := upsertReleasePage(ctx, s.db, stmt, assetStmt, owner, repo, releases, latestPublishedAt)
		if upsertErr != nil {
			return fmt.Errorf("upserting release page for %s/%s: %w", owner, repo, upsertErr)
		}

		slog.Debug("releases done", "org", owner, "repo", repo, "count", len(releases))

		if seenOld || resp.NextPage == 0 {
			break
		}
		opt.Page = resp.NextPage
	}

	return nil
}

func upsertReleasePage(ctx context.Context, db DBTX, stmt, assetStmt *sql.Stmt, owner, repo string, releases []*github.RepositoryRelease, latestPublishedAt string) (bool, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("error starting release tx: %w", err)
	}
	defer rollbackTransaction(tx)

	txStmt := tx.Stmt(stmt)
	defer txStmt.Close()
	txAssetStmt := tx.Stmt(assetStmt)
	defer txAssetStmt.Close()

	// Sort by published_at desc so the break-on-old short-circuit terminates
	// only AFTER every newer release on the page has been considered. The
	// previous alphabetical-by-tag sort scrambled chronology — once any
	// release was in the DB, a tag like "v0.1.0" would sort first, look
	// older than the latest stored published_at, trigger the break, and
	// silently drop newer tags later in the alphabet.
	slices.SortFunc(releases, func(a, b *github.RepositoryRelease) int {
		var ap, bp time.Time
		if a.PublishedAt != nil {
			ap = a.PublishedAt.Time
		}
		if b.PublishedAt != nil {
			bp = b.PublishedAt.Time
		}
		return bp.Compare(ap)
	})

	seenOld := false
	for _, r := range releases {
		tag := r.GetTagName()
		name := r.GetName()
		var publishedAt string
		if r.PublishedAt != nil {
			publishedAt = r.PublishedAt.Format("2006-01-02T15:04:05Z")
		}
		pre := 0
		if r.GetPrerelease() {
			pre = 1
		}

		if latestPublishedAt != "" && publishedAt != "" && publishedAt < latestPublishedAt {
			seenOld = true
			break
		}

		var publishedAtParam any
		if publishedAt != "" {
			publishedAtParam = publishedAt
		}

		if _, execErr := txStmt.ExecContext(ctx,
			owner, repo, tag, name, publishedAtParam, pre,
			name, publishedAtParam, pre,
		); execErr != nil {
			rollbackTransaction(tx)
			return false, fmt.Errorf("error inserting release %s: %w", tag, execErr)
		}

		slices.SortFunc(r.Assets, func(a, b *github.ReleaseAsset) int {
			return strings.Compare(a.GetName(), b.GetName())
		})
		for _, a := range r.Assets {
			aName := a.GetName()
			if aName == "" {
				continue
			}
			if _, execErr := txAssetStmt.ExecContext(ctx,
				owner, repo, tag, aName, a.GetContentType(), a.GetSize(), a.GetDownloadCount(),
				a.GetContentType(), a.GetSize(), a.GetDownloadCount(),
			); execErr != nil {
				rollbackTransaction(tx)
				return false, fmt.Errorf("error inserting release asset %s/%s: %w", tag, aName, execErr)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("error committing release tx: %w", err)
	}

	return seenOld, nil
}

func (s *Store) ImportAllReleases(ctx context.Context, token string) error {
	if s.db == nil {
		return data.ErrDBNotInitialized
	}

	list, err := s.GetAllOrgRepos(ctx)
	if err != nil {
		return fmt.Errorf("error getting org/repo list: %w", err)
	}

	for _, r := range list {
		if err := s.ImportReleases(ctx, token, r.Org, r.Repo); err != nil {
			slog.Error("releases failed", "org", r.Org, "repo", r.Repo, "error", err)
		}
	}

	return nil
}

func (s *Store) GetReleaseCadence(ctx context.Context, org, repo, entity *string, days int) (*data.ReleaseCadenceSeries, error) {
	if s.db == nil {
		return nil, data.ErrDBNotInitialized
	}

	gran := AutoGranularity(days)
	since := sinceDate(days)

	// $1 = since fixed; queryBuilder starts at $2 for org/repo.
	qb := newQueryBuilder(2)
	qb.addOptional("org", org)
	qb.addOptional("repo", repo)
	cadenceQuery := fmt.Sprintf(selectReleaseCadenceTpl,
		GroupExpr(gran, "published_at"), qb.whereClause())

	args := make([]any, 0, 1+len(qb.args))
	args = append(args, since)
	args = append(args, qb.args...)
	rows, err := s.db.QueryContext(ctx, cadenceQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query release cadence: %w", err)
	}
	defer rows.Close()

	sr := &data.ReleaseCadenceSeries{
		Labels:      make([]string, 0),
		Total:       make([]int, 0),
		Stable:      make([]int, 0),
		Deployments: make([]int, 0),
	}

	for rows.Next() {
		var label string
		var total, stable int
		if scanErr := rows.Scan(&label, &total, &stable); scanErr != nil {
			return nil, fmt.Errorf("failed to scan release cadence row: %w", scanErr)
		}
		sr.Labels = append(sr.Labels, label)
		sr.Total = append(sr.Total, total)
		sr.Stable = append(sr.Stable, stable)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating rows: %w", err)
	}

	if len(sr.Labels) > 0 {
		sr.Deployments = append(sr.Deployments, sr.Total...)
		gf := newGapFiller(days, sr.Labels)
		sr.Labels = gf.periods
		sr.Total = gf.fillInt(sr.Total)
		sr.Stable = gf.fillInt(sr.Stable)
		sr.Deployments = gf.fillInt(sr.Deployments)
		return sr, nil
	}

	// Fallback: count merged PRs as deployment proxy when no releases exist.
	// $1 = since fixed; queryBuilder starts at $2 for e.org / e.repo / d.entity.
	fbQb := newQueryBuilder(2)
	fbQb.addOptional("e.org", org)
	fbQb.addOptional("e.repo", repo)
	if entity != nil {
		fbQb.clauses = append(fbQb.clauses,
			fmt.Sprintf("COALESCE(d.entity, '') = $%d", fbQb.paramIdx))
		fbQb.args = append(fbQb.args, *entity)
		fbQb.paramIdx++
	}
	fallbackQuery := fmt.Sprintf(selectMergedPRDeploymentsTpl,
		GroupExpr(gran, "e.merged_at"), fbQb.whereClause())

	fbArgs := make([]any, 0, 1+len(fbQb.args))
	fbArgs = append(fbArgs, since)
	fbArgs = append(fbArgs, fbQb.args...)
	fallbackRows, err := s.db.QueryContext(ctx, fallbackQuery, fbArgs...)
	if err != nil {
		return nil, fmt.Errorf("failed to query merged PR deployments: %w", err)
	}
	defer fallbackRows.Close()

	for fallbackRows.Next() {
		var label string
		var cnt int
		if scanErr := fallbackRows.Scan(&label, &cnt); scanErr != nil {
			return nil, fmt.Errorf("failed to scan merged PR deployment row: %w", scanErr)
		}
		sr.Labels = append(sr.Labels, label)
		sr.Deployments = append(sr.Deployments, cnt)
	}

	if err := fallbackRows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating rows: %w", err)
	}

	gf := newGapFiller(days, sr.Labels)
	sr.Labels = gf.periods
	sr.Total = gf.fillInt(sr.Total)
	sr.Stable = gf.fillInt(sr.Stable)
	sr.Deployments = gf.fillInt(sr.Deployments)

	return sr, nil
}

func (s *Store) GetReleaseDownloads(ctx context.Context, org, repo *string, days int) (*data.ReleaseDownloadsSeries, error) { //nolint:dupl,nolintlint
	if s.db == nil {
		return nil, data.ErrDBNotInitialized
	}

	gran := AutoGranularity(days)
	since := sinceDate(days)

	// $1 = since fixed; queryBuilder starts at $2 for ra.org / ra.repo.
	qb := newQueryBuilder(2)
	qb.addOptional("ra.org", org)
	qb.addOptional("ra.repo", repo)
	query := fmt.Sprintf(selectReleaseDownloadsTpl,
		GroupExpr(gran, "r.published_at"), qb.whereClause())

	args := make([]any, 0, 1+len(qb.args))
	args = append(args, since)
	args = append(args, qb.args...)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query release downloads: %w", err)
	}
	defer rows.Close()

	sr := &data.ReleaseDownloadsSeries{
		Labels:    make([]string, 0),
		Downloads: make([]int, 0),
	}

	for rows.Next() {
		var label string
		var downloads int
		if err := rows.Scan(&label, &downloads); err != nil {
			return nil, fmt.Errorf("failed to scan release downloads row: %w", err)
		}
		sr.Labels = append(sr.Labels, label)
		sr.Downloads = append(sr.Downloads, downloads)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating rows: %w", err)
	}

	gf := newGapFiller(days, sr.Labels)
	sr.Labels = gf.periods
	sr.Downloads = gf.fillInt(sr.Downloads)

	return sr, nil
}

func (s *Store) GetReleaseDownloadsByTag(ctx context.Context, org, repo *string, days int) (*data.ReleaseDownloadsByTagSeries, error) {
	if s.db == nil {
		return nil, data.ErrDBNotInitialized
	}

	since := sinceDate(days)

	// Both CTEs share $1 = since but use different table aliases for the
	// org/repo filter. Build two queryBuilders that start their parameter
	// numbering AFTER $1 — the first builds `recent` clauses ($2+), the
	// second builds `top` clauses (numbered after the first).
	qbRecent := newQueryBuilder(2)
	qbRecent.addOptional("r.org", org)
	qbRecent.addOptional("r.repo", repo)

	qbTop := newQueryBuilder(qbRecent.nextParam())
	qbTop.addOptional("ra.org", org)
	qbTop.addOptional("ra.repo", repo)

	query := fmt.Sprintf(selectReleaseDownloadsByTagTpl,
		qbRecent.whereClause(), qbTop.whereClause())

	args := make([]any, 0, 1+len(qbRecent.args)+len(qbTop.args))
	args = append(args, since)
	args = append(args, qbRecent.args...)
	args = append(args, qbTop.args...)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query release downloads by tag: %w", err)
	}
	defer rows.Close()

	sr := &data.ReleaseDownloadsByTagSeries{
		Tags:      make([]string, 0),
		Downloads: make([]int, 0),
	}

	for rows.Next() {
		var tag string
		var downloads int
		if err := rows.Scan(&tag, &downloads); err != nil {
			return nil, fmt.Errorf("failed to scan release downloads by tag row: %w", err)
		}
		sr.Tags = append(sr.Tags, tag)
		sr.Downloads = append(sr.Downloads, downloads)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating rows: %w", err)
	}

	return sr, nil
}
