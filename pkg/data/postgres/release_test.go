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
	"testing"
	"time"

	"github.com/google/go-github/v83/github"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetReleaseCadence_EmptyDB(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)
	series, err := store.GetReleaseCadence(ctx, nil, nil, nil, 180)
	require.NoError(t, err)
	require.NotNil(t, series)
	for _, v := range series.Total {
		assert.Equal(t, 0, v)
	}
}

func TestGetReleaseCadence_NilDB(t *testing.T) {
	ctx := context.Background()
	s := &Store{db: nil}
	_, err := s.GetReleaseCadence(ctx, nil, nil, nil, 180)
	assert.Error(t, err)
}

func TestGetReleaseCadence_WithData(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)

	_, err := store.db.ExecContext(ctx, `INSERT INTO devpulse_release(org, repo, tag, name, published_at, prerelease)
		VALUES
		('org1', 'repo1', 'v1.0.0', 'Release 1', '2025-01-15T00:00:00Z', 0),
		('org1', 'repo1', 'v1.1.0-rc1', 'RC1', '2025-01-20T00:00:00Z', 1),
		('org1', 'repo1', 'v1.1.0', 'Release 1.1', '2025-02-10T00:00:00Z', 0)`)
	require.NoError(t, err)

	series, err := store.GetReleaseCadence(ctx, nil, nil, nil, 730)
	require.NoError(t, err)

	// January: 2 total (1 stable + 1 prerelease)
	idx := findPeriodIdx(t, series.Labels, "2025-01")
	assert.Equal(t, 2, series.Total[idx])
	assert.Equal(t, 1, series.Stable[idx])

	// February: 1 total, 1 stable
	idx2 := findPeriodIdx(t, series.Labels, "2025-02")
	assert.Equal(t, 1, series.Total[idx2])
	assert.Equal(t, 1, series.Stable[idx2])
}

func TestGetReleaseCadence_WithFilter(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)

	_, err := store.db.ExecContext(ctx, `INSERT INTO devpulse_release(org, repo, tag, name, published_at, prerelease)
		VALUES
		('org1', 'repo1', 'v1.0.0', 'Release 1', '2025-01-15T00:00:00Z', 0),
		('org2', 'repo2', 'v2.0.0', 'Release 2', '2025-01-20T00:00:00Z', 0)`)
	require.NoError(t, err)

	org := "org1"
	series, err := store.GetReleaseCadence(ctx, &org, nil, nil, 730)
	require.NoError(t, err)
	idx := findPeriodIdx(t, series.Labels, "2025-01")
	assert.Equal(t, 1, series.Total[idx])
}

func TestGetReleaseDownloads_NilDB(t *testing.T) {
	ctx := context.Background()
	s := &Store{db: nil}
	_, err := s.GetReleaseDownloads(ctx, nil, nil, 180)
	assert.Error(t, err)
}

func TestGetReleaseDownloads_EmptyDB(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)
	series, err := store.GetReleaseDownloads(ctx, nil, nil, 180)
	require.NoError(t, err)
	require.NotNil(t, series)
	for _, v := range series.Downloads {
		assert.Equal(t, 0, v)
	}
}

func TestGetReleaseDownloads_WithData(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)

	_, err := store.db.ExecContext(ctx, `INSERT INTO devpulse_release(org, repo, tag, name, published_at, prerelease)
		VALUES
		('org1', 'repo1', 'v1.0.0', 'Release 1', '2025-01-15T00:00:00Z', 0),
		('org1', 'repo1', 'v1.1.0', 'Release 1.1', '2025-02-10T00:00:00Z', 0)`)
	require.NoError(t, err)

	_, err = store.db.ExecContext(ctx, `INSERT INTO devpulse_release_asset(org, repo, tag, name, content_type, size, download_count)
		VALUES
		('org1', 'repo1', 'v1.0.0', 'app_linux_amd64.tar.gz', 'application/gzip', 1000, 150),
		('org1', 'repo1', 'v1.0.0', 'app_darwin_arm64.tar.gz', 'application/gzip', 1000, 50),
		('org1', 'repo1', 'v1.1.0', 'app_linux_amd64.tar.gz', 'application/gzip', 1000, 30)`)
	require.NoError(t, err)

	series, err := store.GetReleaseDownloads(ctx, nil, nil, 730)
	require.NoError(t, err)

	idx := findPeriodIdx(t, series.Labels, "2025-01")
	assert.Equal(t, 200, series.Downloads[idx])

	idx2 := findPeriodIdx(t, series.Labels, "2025-02")
	assert.Equal(t, 30, series.Downloads[idx2])
}

func TestGetReleaseDownloads_WithFilter(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)

	_, err := store.db.ExecContext(ctx, `INSERT INTO devpulse_release(org, repo, tag, name, published_at, prerelease)
		VALUES
		('org1', 'repo1', 'v1.0.0', 'Release 1', '2025-01-15T00:00:00Z', 0),
		('org2', 'repo2', 'v2.0.0', 'Release 2', '2025-01-20T00:00:00Z', 0)`)
	require.NoError(t, err)

	_, err = store.db.ExecContext(ctx, `INSERT INTO devpulse_release_asset(org, repo, tag, name, content_type, size, download_count)
		VALUES
		('org1', 'repo1', 'v1.0.0', 'app.tar.gz', 'application/gzip', 1000, 100),
		('org2', 'repo2', 'v2.0.0', 'app.tar.gz', 'application/gzip', 1000, 200)`)
	require.NoError(t, err)

	org := "org1"
	series, err := store.GetReleaseDownloads(ctx, &org, nil, 730)
	require.NoError(t, err)
	idx := findPeriodIdx(t, series.Labels, "2025-01")
	assert.Equal(t, 100, series.Downloads[idx])
}

func TestGetReleaseDownloadsByTag_NilDB(t *testing.T) {
	ctx := context.Background()
	s := &Store{db: nil}
	_, err := s.GetReleaseDownloadsByTag(ctx, nil, nil, 180)
	assert.Error(t, err)
}

func TestGetReleaseDownloadsByTag_EmptyDB(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)
	series, err := store.GetReleaseDownloadsByTag(ctx, nil, nil, 180)
	require.NoError(t, err)
	assert.Empty(t, series.Tags)
}

func TestGetReleaseDownloadsByTag_WithData(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)

	// Insert 11 releases spanning recent months
	_, err := store.db.ExecContext(ctx, `INSERT INTO devpulse_release(org, repo, tag, name, published_at, prerelease) VALUES
		('org1', 'repo1', 'v0.1.0', 'R0.1', '2025-01-01T00:00:00Z', 0),
		('org1', 'repo1', 'v0.2.0', 'R0.2', '2025-02-01T00:00:00Z', 0),
		('org1', 'repo1', 'v0.3.0', 'R0.3', '2025-03-01T00:00:00Z', 0),
		('org1', 'repo1', 'v0.4.0', 'R0.4', '2025-04-01T00:00:00Z', 0),
		('org1', 'repo1', 'v0.5.0', 'R0.5', '2025-05-01T00:00:00Z', 0),
		('org1', 'repo1', 'v0.6.0', 'R0.6', '2025-06-01T00:00:00Z', 0),
		('org1', 'repo1', 'v0.7.0', 'R0.7', '2025-07-01T00:00:00Z', 0),
		('org1', 'repo1', 'v0.8.0', 'R0.8', '2025-08-01T00:00:00Z', 0),
		('org1', 'repo1', 'v0.9.0', 'R0.9', '2025-09-01T00:00:00Z', 0),
		('org1', 'repo1', 'v1.0.0', 'R1.0', '2025-10-01T00:00:00Z', 0),
		('org1', 'repo1', 'v1.1.0', 'R1.1', '2025-11-01T00:00:00Z', 0)`)
	require.NoError(t, err)

	_, err = store.db.ExecContext(ctx, `INSERT INTO devpulse_release_asset(org, repo, tag, name, content_type, size, download_count) VALUES
		('org1', 'repo1', 'v0.1.0', 'app.tar.gz', 'application/gzip', 100, 5000),
		('org1', 'repo1', 'v0.2.0', 'app.tar.gz', 'application/gzip', 100, 10),
		('org1', 'repo1', 'v0.3.0', 'app.tar.gz', 'application/gzip', 100, 20),
		('org1', 'repo1', 'v0.4.0', 'app.tar.gz', 'application/gzip', 100, 30),
		('org1', 'repo1', 'v0.5.0', 'app.tar.gz', 'application/gzip', 100, 40),
		('org1', 'repo1', 'v0.6.0', 'app.tar.gz', 'application/gzip', 100, 50),
		('org1', 'repo1', 'v0.7.0', 'app.tar.gz', 'application/gzip', 100, 60),
		('org1', 'repo1', 'v0.8.0', 'app.tar.gz', 'application/gzip', 100, 70),
		('org1', 'repo1', 'v0.9.0', 'app.tar.gz', 'application/gzip', 100, 80),
		('org1', 'repo1', 'v1.0.0', 'app.tar.gz', 'application/gzip', 100, 90),
		('org1', 'repo1', 'v1.1.0', 'app.tar.gz', 'application/gzip', 100, 100)`)
	require.NoError(t, err)

	series, err := store.GetReleaseDownloadsByTag(ctx, nil, nil, 730)
	require.NoError(t, err)

	// 9 recent (v0.3.0..v1.1.0) + top (v0.1.0) = 10
	require.Len(t, series.Tags, 10)

	// First entry should be v0.1.0 (oldest by published_at, pulled in as top)
	assert.Equal(t, "v0.1.0", series.Tags[0])
	assert.Equal(t, 5000, series.Downloads[0])

	// Last entry should be v1.1.0 (most recent)
	assert.Equal(t, "v1.1.0", series.Tags[9])
	assert.Equal(t, 100, series.Downloads[9])
}

func TestGetReleaseCadence_WithDeployments(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)

	_, err := store.db.ExecContext(ctx, `INSERT INTO devpulse_release(org, repo, tag, name, published_at, prerelease)
		VALUES ('org1', 'repo1', 'v1.0', 'v1.0', '2025-01-15T00:00:00Z', 0)`)
	require.NoError(t, err)

	series, err := store.GetReleaseCadence(ctx, nil, nil, nil, 730)
	require.NoError(t, err)
	require.NotEmpty(t, series.Labels)
	idx := findPeriodIdx(t, series.Labels, "2025-01")
	assert.Equal(t, 1, series.Deployments[idx])
}

func TestGetReleaseCadence_MergeFallback(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)

	_, err := store.db.ExecContext(ctx, `INSERT INTO devpulse_developer(username, full_name) VALUES ('alice', 'Alice')`)
	require.NoError(t, err)

	// No releases for org2 -- insert merged PRs instead
	_, err = store.db.ExecContext(ctx, `INSERT INTO devpulse_event(org, repo, username, type, date, url, mentions, labels, state, created_at, merged_at)
		VALUES ('org2', 'repo2', 'alice', 'pr', '2025-01-10', 'http://a', '', '', 'merged', '2025-01-10T10:00:00Z', '2025-01-10T12:00:00Z'),
		       ('org2', 'repo2', 'alice', 'pr', '2025-01-11', 'http://b', '', '', 'merged', '2025-01-11T10:00:00Z', '2025-01-11T12:00:00Z')`)
	require.NoError(t, err)

	org := "org2"
	repo := "repo2"
	series, err := store.GetReleaseCadence(ctx, &org, &repo, nil, 730)
	require.NoError(t, err)
	require.NotEmpty(t, series.Labels)
	idx := findPeriodIdx(t, series.Labels, "2025-01")
	assert.Equal(t, 2, series.Deployments[idx])
}

func TestGetReleaseDownloadsByTag_Dedup(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)

	// Only 3 releases -- the top downloaded IS in the recent 9
	_, err := store.db.ExecContext(ctx, `INSERT INTO devpulse_release(org, repo, tag, name, published_at, prerelease) VALUES
		('org1', 'repo1', 'v1.0.0', 'R1', '2025-10-01T00:00:00Z', 0),
		('org1', 'repo1', 'v1.1.0', 'R2', '2025-11-01T00:00:00Z', 0),
		('org1', 'repo1', 'v1.2.0', 'R3', '2025-12-01T00:00:00Z', 0)`)
	require.NoError(t, err)

	_, err = store.db.ExecContext(ctx, `INSERT INTO devpulse_release_asset(org, repo, tag, name, content_type, size, download_count) VALUES
		('org1', 'repo1', 'v1.0.0', 'app.tar.gz', 'application/gzip', 100, 10),
		('org1', 'repo1', 'v1.1.0', 'app.tar.gz', 'application/gzip', 100, 500),
		('org1', 'repo1', 'v1.2.0', 'app.tar.gz', 'application/gzip', 100, 20)`)
	require.NoError(t, err)

	series, err := store.GetReleaseDownloadsByTag(ctx, nil, nil, 730)
	require.NoError(t, err)

	// Only 3 entries -- no duplicate for v1.1.0
	require.Len(t, series.Tags, 3)
}

// TestUpsertReleasePage_PreservesNewerOutOfOrderTags locks in the sort fix.
// Under the previous alphabetical-by-tag sort + break-on-old logic, a page
// containing a newer release whose tag sorts earlier than the latest stored
// release would be silently skipped: the older tag was iterated first, hit
// the break, and never reached the newer one.
func TestUpsertReleasePage_PreservesNewerOutOfOrderTags(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)

	const (
		owner = "ai-dynamo"
		repo  = "dynamo"
	)

	// Seed the latest release we'd already have in the DB.
	latestPublishedAt := "2026-03-17T14:43:12Z"
	_, err := store.db.ExecContext(ctx,
		`INSERT INTO devpulse_release(org, repo, tag, name, published_at, prerelease)
		 VALUES ($1, $2, 'v1.1.0-dev.1', 'Dynamo v1.1.0-dev.1', $3, 1)`,
		owner, repo, latestPublishedAt)
	require.NoError(t, err)

	stmt, err := store.db.PrepareContext(ctx, insertReleaseSQL)
	require.NoError(t, err)
	defer stmt.Close()
	assetStmt, err := store.db.PrepareContext(ctx, insertReleaseAssetSQL)
	require.NoError(t, err)
	defer assetStmt.Close()

	mkRelease := func(tag string, publishedAt time.Time, prerelease bool) *github.RepositoryRelease {
		ts := github.Timestamp{Time: publishedAt}
		return &github.RepositoryRelease{
			TagName:     github.Ptr(tag),
			Name:        github.Ptr("Dynamo " + tag),
			PublishedAt: &ts,
			Prerelease:  github.Ptr(prerelease),
		}
	}

	// Page intentionally provided in arbitrary order. v1.0.2 (2026-04-27)
	// is NEWER than the seeded latest, but its tag sorts BEFORE v1.1.0-dev.1
	// alphabetically — exactly the scenario that broke the import.
	page := []*github.RepositoryRelease{
		mkRelease("v0.5.0", mustTime("2025-09-01T00:00:00Z"), false),
		mkRelease("v0.7.0", mustTime("2025-11-26T00:00:00Z"), false),
		mkRelease("v1.0.0", mustTime("2026-03-13T00:00:00Z"), false),
		mkRelease("v1.0.1", mustTime("2026-03-16T00:00:00Z"), false),
		mkRelease("v1.0.2", mustTime("2026-04-27T00:00:00Z"), false),
	}

	_, err = upsertReleasePage(ctx, store.db, stmt, assetStmt, owner, repo, page, latestPublishedAt)
	require.NoError(t, err)

	var got string
	err = store.db.QueryRowContext(ctx,
		`SELECT published_at FROM devpulse_release WHERE org=$1 AND repo=$2 AND tag='v1.0.2'`,
		owner, repo).Scan(&got)
	require.NoError(t, err, "v1.0.2 must be inserted even when it sorts before the latest tag alphabetically")
	assert.Equal(t, "2026-04-27T00:00:00Z", got)
}

func mustTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}
