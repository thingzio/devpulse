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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetAllOrgRepos_WithData(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)

	_, err := store.db.ExecContext(ctx, `INSERT INTO devpulse_repo_meta(org, repo, stars, forks, open_issues, language, license, archived)
		VALUES ('org1', 'repo1', 10, 5, 1, 'Go', 'MIT', 0),
		       ('org1', 'repo2', 20, 3, 0, 'Rust', 'Apache-2.0', 0),
		       ('org2', 'repo3', 5, 1, 2, 'Python', 'BSD', 0)`)
	require.NoError(t, err)

	list, err := store.GetAllOrgRepos(ctx)
	require.NoError(t, err)
	assert.Len(t, list, 3)
	assert.Equal(t, "org1", list[0].Org)
	assert.Equal(t, "repo1", list[0].Repo)
	assert.Equal(t, "org2", list[2].Org)
}

func TestGetAllOrgRepos_NilDB(t *testing.T) {
	ctx := context.Background()
	s := &Store{db: nil}
	_, err := s.GetAllOrgRepos(ctx)
	assert.Error(t, err)
}

func TestGetOrgLike(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)
	seedTestData(t, store)
	items, err := store.GetOrgLike(ctx, "test", 10)
	require.NoError(t, err)
	assert.NotEmpty(t, items)
}

func TestGetOrgLike_EmptyQuery(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)
	_, err := store.GetOrgLike(ctx, "", 10)
	assert.Error(t, err)
}

func TestGetOrgLike_NilDB(t *testing.T) {
	ctx := context.Background()
	s := &Store{db: nil}
	_, err := s.GetOrgLike(ctx, "test", 10)
	assert.Error(t, err)
}

func TestGetAllOrgRepos_EmptyDB(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)
	repos, err := store.GetAllOrgRepos(ctx)
	require.NoError(t, err)
	assert.Empty(t, repos)
}

func TestGetDeveloperPercentages_NilDB(t *testing.T) {
	ctx := context.Background()
	s := &Store{db: nil}
	_, err := s.GetDeveloperPercentages(ctx, nil, nil, nil, nil, 180)
	assert.Error(t, err)
}

func TestGetEntityPercentages_NilDB(t *testing.T) {
	ctx := context.Background()
	s := &Store{db: nil}
	_, err := s.GetEntityPercentages(ctx, nil, nil, nil, nil, 180)
	assert.Error(t, err)
}

func TestGetDeveloperPercentages(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)
	seedTestData(t, store)
	results, err := store.GetDeveloperPercentages(ctx, nil, nil, nil, []string{}, 730)
	require.NoError(t, err)
	assert.NotEmpty(t, results)
}

func TestGetEntityPercentages(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)
	seedTestData(t, store)
	results, err := store.GetEntityPercentages(ctx, nil, nil, nil, []string{}, 730)
	require.NoError(t, err)
	assert.NotEmpty(t, results)
}

func TestSearchDeveloperUsernames_NilDB(t *testing.T) {
	ctx := context.Background()
	s := &Store{db: nil}
	_, err := s.SearchDeveloperUsernames(ctx, "dev", nil, nil, 180, 10)
	assert.Error(t, err)
}

func TestSearchDeveloperUsernames_EmptyQuery(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)
	_, err := store.SearchDeveloperUsernames(ctx, "", nil, nil, 180, 10)
	assert.Error(t, err)
}

func TestSearchDeveloperUsernames_EmptyDB(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)
	results, err := store.SearchDeveloperUsernames(ctx, "dev", nil, nil, 180, 10)
	require.NoError(t, err)
	assert.Empty(t, results)
}

func TestSearchDeveloperUsernames_WithData(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)
	seedTestData(t, store)
	results, err := store.SearchDeveloperUsernames(ctx, "dev", nil, nil, 730, 10)
	require.NoError(t, err)
	assert.NotEmpty(t, results)
	for _, r := range results {
		assert.Contains(t, r, "dev")
	}
}
