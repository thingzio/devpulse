package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/google/go-github/v83/github"
	"github.com/thingzio/devpulse/pkg/data"
	"github.com/thingzio/devpulse/pkg/data/ghutil"
	"github.com/thingzio/devpulse/pkg/net"
)

const (
	// insertReleaseSQL: 9 params
	insertReleaseSQL = `INSERT INTO release (org, repo, tag, name, published_at, prerelease)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT(org, repo, tag) DO UPDATE SET
			name = $7, published_at = $8, prerelease = $9
	`

	// selectReleaseCadenceTpl: $1=org, $2=repo, $3=since
	// %s = GroupExpr(gran, "published_at")
	selectReleaseCadenceTpl = `SELECT
			%s AS period,
			COUNT(*) AS total,
			SUM(CASE WHEN prerelease = 0 THEN 1 ELSE 0 END) AS stable
		FROM release
		WHERE org = COALESCE($1, org)
		  AND repo = COALESCE($2, repo)
		  AND published_at >= $3
		GROUP BY period
		ORDER BY period
	`

	// insertReleaseAssetSQL: 10 params
	insertReleaseAssetSQL = `INSERT INTO release_asset (org, repo, tag, name, content_type, size, download_count)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT(org, repo, tag, name) DO UPDATE SET
			content_type = $8, size = $9, download_count = $10
	`

	// selectReleaseDownloadsTpl: $1=org, $2=repo, $3=since
	// %s = GroupExpr(gran, "r.published_at")
	selectReleaseDownloadsTpl = `SELECT
			%s AS period,
			SUM(ra.download_count) AS downloads
		FROM release_asset ra
		JOIN release r ON ra.org = r.org AND ra.repo = r.repo AND ra.tag = r.tag
		WHERE ra.org = COALESCE($1, ra.org)
		  AND ra.repo = COALESCE($2, ra.repo)
		  AND r.published_at >= $3
		GROUP BY period
		ORDER BY period
	`

	// selectMergedPRDeploymentsTpl: $1=org, $2=repo, $3=entity, $4=since
	// %s = GroupExpr(gran, "e.merged_at")
	selectMergedPRDeploymentsTpl = `SELECT
			%s AS period,
			COUNT(*) AS cnt
		FROM event e
		JOIN developer d ON e.username = d.username
		WHERE e.type = 'pr'
		  AND e.state = 'merged'
		  AND e.merged_at IS NOT NULL
		  AND e.org = COALESCE($1, e.org)
		  AND e.repo = COALESCE($2, e.repo)
		  AND COALESCE(d.entity, '') = COALESCE($3, COALESCE(d.entity, ''))
		  AND e.merged_at >= $4
		  ` + botExcludeTpl + `
		GROUP BY period
		ORDER BY period
	`

	// selectLatestReleaseSQL: $1=org, $2=repo
	selectLatestReleaseSQL = `SELECT COALESCE(MAX(published_at), '')
		FROM release
		WHERE org = $1 AND repo = $2
	`

	// selectReleaseDownloadsByTagSQL: $1=org, $2=repo, $3=since, $4=org, $5=repo, $6=since
	selectReleaseDownloadsByTagSQL = `WITH recent AS (
			SELECT r.org, r.repo, r.tag, r.published_at
			FROM release r
			WHERE r.org = COALESCE($1, r.org)
			  AND r.repo = COALESCE($2, r.repo)
			  AND r.published_at >= $3
			ORDER BY r.published_at DESC
			LIMIT 9
		), top AS (
			SELECT ra.org, ra.repo, ra.tag, r.published_at
			FROM release_asset ra
			JOIN release r ON ra.org = r.org AND ra.repo = r.repo AND ra.tag = r.tag
			WHERE ra.org = COALESCE($4, ra.org)
			  AND ra.repo = COALESCE($5, ra.repo)
			  AND r.published_at >= $6
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
		LEFT JOIN release_asset ra ON c.org = ra.org AND c.repo = ra.repo AND c.tag = ra.tag
		GROUP BY c.tag, c.published_at
		ORDER BY c.published_at
	`
)

func (s *Store) ImportReleases(ctx context.Context, token, owner, repo string) error {
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
			return err
		}

		if len(releases) == 0 {
			break
		}

		seenOld, upsertErr := upsertReleasePage(ctx, s.db, stmt, assetStmt, owner, repo, releases, latestPublishedAt)
		if upsertErr != nil {
			return upsertErr
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

	txStmt := tx.Stmt(stmt)
	defer txStmt.Close()
	txAssetStmt := tx.Stmt(assetStmt)
	defer txAssetStmt.Close()

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

		if _, execErr := txStmt.ExecContext(ctx,
			owner, repo, tag, name, publishedAt, pre,
			name, publishedAt, pre,
		); execErr != nil {
			rollbackTransaction(tx)
			return false, fmt.Errorf("error inserting release %s: %w", tag, execErr)
		}

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
	cadenceQuery := fmt.Sprintf(selectReleaseCadenceTpl, GroupExpr(gran, "published_at"))
	since := sinceDate(days)

	rows, err := s.db.QueryContext(ctx, cadenceQuery, org, repo, since)
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
		return sr, nil
	}

	fallbackQuery := fmt.Sprintf(selectMergedPRDeploymentsTpl, GroupExpr(gran, "e.merged_at"))
	fallbackRows, err := s.db.QueryContext(ctx, fallbackQuery, org, repo, entity, since)
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

	return sr, nil
}

func (s *Store) GetReleaseDownloads(ctx context.Context, org, repo *string, days int) (*data.ReleaseDownloadsSeries, error) { //nolint:dupl,nolintlint
	if s.db == nil {
		return nil, data.ErrDBNotInitialized
	}

	gran := AutoGranularity(days)
	query := fmt.Sprintf(selectReleaseDownloadsTpl, GroupExpr(gran, "r.published_at"))
	since := sinceDate(days)

	rows, err := s.db.QueryContext(ctx, query, org, repo, since)
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

	return sr, nil
}

func (s *Store) GetReleaseDownloadsByTag(ctx context.Context, org, repo *string, days int) (*data.ReleaseDownloadsByTagSeries, error) {
	if s.db == nil {
		return nil, data.ErrDBNotInitialized
	}

	since := sinceDate(days)

	rows, err := s.db.QueryContext(ctx, selectReleaseDownloadsByTagSQL, org, repo, since, org, repo, since)
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
