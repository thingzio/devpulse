package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/google/go-github/v83/github"
	"github.com/thingzio/devpulse/pkg/data"
	"github.com/thingzio/devpulse/pkg/data/ghutil"
	"github.com/thingzio/devpulse/pkg/net"
)

const (
	// upsertContainerVersionSQL: 6 params
	upsertContainerVersionSQL = `INSERT INTO devpulse_container_version (org, repo, package, version_id, tag, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT(org, repo, package, version_id) DO UPDATE SET
			tag = excluded.tag,
			created_at = excluded.created_at
	`

	// selectLatestContainerVersionSQL: $1=org, $2=repo, $3=package
	selectLatestContainerVersionSQL = `SELECT COALESCE(MAX(created_at), '')
		FROM devpulse_container_version
		WHERE org = $1 AND repo = $2 AND package = $3
	`

	// selectContainerActivityTpl: $1=since fixed; first %s = GroupExpr(gran),
	// second %s = queryBuilder whereClause for cv.org / cv.repo. Replaces
	// COALESCE anti-pattern so the (org, repo, ...) primary key can be used.
	selectContainerActivityTpl = `SELECT
		%s AS period,
		COUNT(*) AS versions
	FROM devpulse_container_version cv
	WHERE cv.created_at >= $1
	  %s
	GROUP BY period
	ORDER BY period
	`
)

func (s *Store) ImportContainerVersions(ctx context.Context, token, org, repo string) error {
	if s.db == nil {
		return data.ErrDBNotInitialized
	}

	client := github.NewClient(net.GetOAuthClient(ctx, token))

	matched, err := listRepoContainerPackages(ctx, client, org, repo)
	if err != nil {
		return fmt.Errorf("listing container packages for %s/%s: %w", org, repo, err)
	}
	if len(matched) == 0 {
		return nil
	}

	total, err := upsertContainerVersions(ctx, client, s.db, org, repo, matched)
	if err != nil {
		return fmt.Errorf("upserting container versions for %s/%s: %w", org, repo, err)
	}

	if total > 0 {
		slog.Info("container versions", "org", org, "repo", repo, "versions", total)
	}

	return nil
}

func listRepoContainerPackages(ctx context.Context, client *github.Client, org, repo string) ([]*github.Package, error) {
	opts := &github.PackageListOptions{
		PackageType: github.Ptr("container"),
		ListOptions: github.ListOptions{PerPage: 100},
	}

	var matched []*github.Package
	var total int

	for {
		packages, resp, err := client.Organizations.ListPackages(ctx, org, opts)
		if err != nil {
			if resp != nil && (resp.StatusCode == 400 || resp.StatusCode == 403 || resp.StatusCode == 404) {
				slog.Debug("container packages not accessible", "org", org, "status", resp.StatusCode)
				return nil, nil
			}
			return nil, fmt.Errorf("listing packages for %s: %w", org, err)
		}
		if err := ghutil.CheckRateLimit(ctx, resp); err != nil {
			return nil, fmt.Errorf("rate limit listing containers: %w", err)
		}

		total += len(packages)

		for _, pkg := range packages {
			if pkg.Repository != nil && strings.EqualFold(pkg.Repository.GetName(), repo) {
				matched = append(matched, pkg)
			} else if pkg.Repository == nil && strings.EqualFold(pkg.GetName(), repo) {
				matched = append(matched, pkg)
			}
		}

		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}

	slog.Debug("container packages", "org", org, "repo", repo, "total", total, "matched", len(matched))
	return matched, nil
}

func upsertContainerVersions(ctx context.Context, client *github.Client, db DBTX, org, repo string, packages []*github.Package) (int, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("beginning container version tx: %w", err)
	}
	defer rollbackTransaction(tx)

	stmt, err := tx.PrepareContext(ctx, upsertContainerVersionSQL)
	if err != nil {
		return 0, fmt.Errorf("preparing container version upsert: %w", err)
	}
	defer stmt.Close()

	var total int
	for _, pkg := range packages {
		pkgName := pkg.GetName()

		var latestCreatedAt string
		if err := tx.QueryRowContext(ctx, selectLatestContainerVersionSQL, org, repo, pkgName).Scan(&latestCreatedAt); err != nil {
			return 0, fmt.Errorf("querying latest container version for %s/%s/%s: %w", org, repo, pkgName, err)
		}

		n, fetchErr := fetchAndStoreVersions(ctx, client, stmt, org, repo, pkgName, latestCreatedAt)
		if fetchErr != nil {
			return 0, fetchErr
		}
		total += n
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("committing container version tx: %w", err)
	}

	return total, nil
}

func fetchAndStoreVersions(ctx context.Context, client *github.Client, stmt *sql.Stmt, org, repo, pkgName, sinceCreatedAt string) (int, error) {
	var count int
	opts := &github.PackageListOptions{
		ListOptions: github.ListOptions{PerPage: 100},
	}

	for {
		versions, resp, err := client.Organizations.PackageGetAllVersions(ctx, org, "container", pkgName, opts)
		if err != nil {
			return 0, fmt.Errorf("listing versions for %s/%s: %w", org, pkgName, err)
		}
		if err := ghutil.CheckRateLimit(ctx, resp); err != nil {
			return 0, fmt.Errorf("rate limit listing container versions: %w", err)
		}

		seenOld := false
		for _, v := range versions {
			tag := extractContainerTag(v)
			createdAt := ""
			if v.CreatedAt != nil {
				createdAt = v.CreatedAt.Format("2006-01-02T15:04:05Z")
			}

			if sinceCreatedAt != "" && createdAt <= sinceCreatedAt {
				seenOld = true
				break
			}

			if _, execErr := stmt.ExecContext(ctx, org, repo, pkgName, v.GetID(), tag, createdAt); execErr != nil {
				return 0, fmt.Errorf("inserting container version %d: %w", v.GetID(), execErr)
			}
			count++
		}

		if seenOld || resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}

	return count, nil
}

func (s *Store) ImportAllContainerVersions(ctx context.Context, token string) error {
	if s.db == nil {
		return data.ErrDBNotInitialized
	}

	list, err := s.GetAllOrgRepos(ctx)
	if err != nil {
		return fmt.Errorf("getting org/repo list: %w", err)
	}

	for _, r := range list {
		if err := s.ImportContainerVersions(ctx, token, r.Org, r.Repo); err != nil {
			slog.Error("container versions failed", "org", r.Org, "repo", r.Repo, "error", err)
		}
	}

	return nil
}

func (s *Store) GetContainerActivity(ctx context.Context, org, repo *string, days int) (*data.ContainerActivitySeries, error) { //nolint:dupl,nolintlint
	if s.db == nil {
		return nil, data.ErrDBNotInitialized
	}

	gran := AutoGranularity(days)
	since := sinceDate(days)

	// $1 = since fixed; queryBuilder starts at $2 for cv.org / cv.repo.
	qb := newQueryBuilder(2)
	qb.addOptional("cv.org", org)
	qb.addOptional("cv.repo", repo)
	query := fmt.Sprintf(selectContainerActivityTpl,
		GroupExpr(gran, "cv.created_at"), qb.whereClause())

	args := make([]any, 0, 1+len(qb.args))
	args = append(args, since)
	args = append(args, qb.args...)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("querying container activity: %w", err)
	}
	defer rows.Close()

	sr := &data.ContainerActivitySeries{
		Labels:   make([]string, 0),
		Versions: make([]int, 0),
	}

	for rows.Next() {
		var label string
		var count int
		if err := rows.Scan(&label, &count); err != nil {
			return nil, fmt.Errorf("scanning container activity row: %w", err)
		}
		sr.Labels = append(sr.Labels, label)
		sr.Versions = append(sr.Versions, count)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating rows: %w", err)
	}

	gf := newGapFiller(days, sr.Labels)
	sr.Labels = gf.periods
	sr.Versions = gf.fillInt(sr.Versions)

	return sr, nil
}

func extractContainerTag(v *github.PackageVersion) string {
	if v.Metadata != nil {
		var meta github.PackageMetadata
		if err := json.Unmarshal(v.Metadata, &meta); err == nil {
			if meta.Container != nil && len(meta.Container.Tags) > 0 {
				return meta.Container.Tags[0]
			}
		}
	}
	if v.ContainerMetadata != nil && v.ContainerMetadata.Tag != nil {
		return v.ContainerMetadata.Tag.GetName()
	}
	return ""
}
