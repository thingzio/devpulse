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
	"regexp"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thingzio/devpulse/pkg/data"
)

func seedTestData(t *testing.T, store *Store) {
	t.Helper()
	ctx := context.Background()
	devs := []*data.Developer{
		{Username: "dev1", FullName: "Dev One", Email: "dev1@google.com", Entity: "GOOGLE"},
		{Username: "dev2", FullName: "Dev Two", Email: "dev2@google.com", Entity: "GOOGLE"},
		{Username: "dev3", FullName: "Dev Three", Email: "dev3@msft.com", Entity: "MICROSOFT"},
	}
	require.NoError(t, store.SaveDevelopers(ctx, devs))

	tx, err := store.db.BeginTx(ctx, nil)
	require.NoError(t, err)
	stmt, err := store.db.PrepareContext(ctx, insertEventSQL)
	require.NoError(t, err)

	events := []data.Event{
		{Org: "testorg", Repo: "testrepo", Username: "dev1", Type: data.EventTypePR, Date: "2026-01-15", URL: "https://github.com/pr/1", Mentions: "", Labels: ""},
		{Org: "testorg", Repo: "testrepo", Username: "dev2", Type: data.EventTypeIssue, Date: "2026-01-16", URL: "https://github.com/issue/1", Mentions: "dev1", Labels: "bug"},
		{Org: "testorg", Repo: "testrepo", Username: "dev3", Type: data.EventTypePR, Date: "2026-01-17", URL: "https://github.com/pr/2", Mentions: "", Labels: ""},
	}

	for _, e := range events {
		_, err = tx.Stmt(stmt).Exec(
			e.Org, e.Repo, e.Username, e.Type, e.Date,
			e.URL, e.Mentions, e.Labels,
			e.State, e.Number, e.CreatedAt, e.ClosedAt, e.MergedAt, e.Additions, e.Deletions,
			e.ChangedFiles, e.Commits, e.Title,
			e.URL, e.Mentions, e.Labels,
			e.State, e.Number, e.CreatedAt, e.ClosedAt, e.MergedAt, e.Additions, e.Deletions,
			e.ChangedFiles, e.Commits, e.Title,
		)
		require.NoError(t, err)
	}
	require.NoError(t, tx.Commit())
}

func TestQueryEntities(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)
	seedTestData(t, store)
	results, err := store.QueryEntities(ctx, "GOOGLE", 10)
	require.NoError(t, err)
	assert.NotEmpty(t, results)
}

func TestGetEntity(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)
	seedTestData(t, store)
	result, err := store.GetEntity(ctx, "GOOGLE")
	require.NoError(t, err)
	assert.Equal(t, 2, result.DeveloperCount)
}

func TestGetEntityLike(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)
	seedTestData(t, store)
	results, err := store.GetEntityLike(ctx, "GOOG", 10)
	require.NoError(t, err)
	assert.NotEmpty(t, results)
}

func TestGetEntityLike_EmptyQuery(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)
	_, err := store.GetEntityLike(ctx, "", 10)
	assert.Error(t, err)
}

func TestGetEntityLike_NilDB(t *testing.T) {
	ctx := context.Background()
	s := &Store{db: nil}
	_, err := s.GetEntityLike(ctx, "test", 10)
	assert.Error(t, err)
}

func TestQueryEntities_NilDB(t *testing.T) {
	ctx := context.Background()
	s := &Store{db: nil}
	_, err := s.QueryEntities(ctx, "test", 10)
	assert.Error(t, err)
}

func TestGetEntity_NilDB(t *testing.T) {
	ctx := context.Background()
	s := &Store{db: nil}
	_, err := s.GetEntity(ctx, "test")
	assert.Error(t, err)
}

func TestCleanEntities(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)
	devs := []*data.Developer{
		{Username: "dev1", FullName: "Dev One", Entity: "Google LLC"},
		{Username: "dev2", FullName: "Dev Two", Entity: "Microsoft Corp"},
	}
	require.NoError(t, store.SaveDevelopers(ctx, devs))

	require.NoError(t, store.CleanEntities(ctx))

	dev, err := store.GetDeveloper(ctx, "dev1")
	require.NoError(t, err)
	assert.Equal(t, "GOOGLE", dev.Entity)
}

func TestCleanEntities_NilDB(t *testing.T) {
	ctx := context.Background()
	s := &Store{db: nil}
	err := s.CleanEntities(ctx)
	assert.Error(t, err)
}

func TestGetEntity_NotFound(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)
	result, err := store.GetEntity(ctx, "NONEXISTENT")
	require.NoError(t, err)
	assert.Equal(t, 0, result.DeveloperCount)
}

func TestQueryEntities_NoMatch(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)
	seedTestData(t, store)
	results, err := store.QueryEntities(ctx, "NONEXISTENT", 10)
	require.NoError(t, err)
	assert.Empty(t, results)
}

func TestCleanEntities_Concurrent(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)

	devs := []*data.Developer{
		{Username: "u1", FullName: "U1", Entity: "  NVIDIA  "},
		{Username: "u2", FullName: "U2", Entity: "Google, Inc."},
	}
	require.NoError(t, store.SaveDevelopers(ctx, devs))

	var wg sync.WaitGroup
	errs := make([]error, 3)
	for i := range 3 {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			errs[idx] = store.CleanEntities(ctx)
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		assert.NoError(t, err, "goroutine %d", i)
	}
}

func TestCleanEntity(t *testing.T) {
	entityRegEx = regexp.MustCompile(nonAlphaNumRegex)

	tests := map[string]string{
		"Google LLC":           "GOOGLE",
		"Hitachi Vantara LLC":  "HITACHI VANTARA",
		"MAX KELSEN PTY. LTD.": "MAX KELSEN PTY",
		"Mercari Inc":          "MERCARI",
		"Some Company Corp.":   "SOME",
		"Big Cars LLC.":        "BIG CARS",
		"International Business Machines Corporation": "IBM",
	}

	for input, expected := range tests {
		val := cleanEntityName(input)
		assert.Equal(t, expected, val)
	}
}
