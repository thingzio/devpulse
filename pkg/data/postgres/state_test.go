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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thingzio/devpulse/pkg/data"
)

func TestGetState_NoExistingState(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)
	min := time.Now().AddDate(0, -6, 0).UTC()
	state, err := store.GetState(ctx, "pr", "testorg", "testrepo", min)
	require.NoError(t, err)
	assert.Equal(t, 1, state.Page)
}

func TestSaveAndGetState(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)
	min := time.Now().AddDate(0, -6, 0).UTC()
	since := time.Now().AddDate(0, -3, 0).UTC()

	s := &data.State{Page: 5, Since: since}
	err := store.SaveState(ctx, "pr", "testorg", "testrepo", s)
	require.NoError(t, err)

	got, err := store.GetState(ctx, "pr", "testorg", "testrepo", min)
	require.NoError(t, err)
	assert.Equal(t, 5, got.Page)
	assert.Nil(t, got.BackfillUntil)
}

func TestSaveState_NilState(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)
	err := store.SaveState(ctx, "pr", "org", "repo", nil)
	assert.Error(t, err)
}

func TestSaveState_EmptyParams(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)
	s := &data.State{Page: 1, Since: time.Now()}
	err := store.SaveState(ctx, "", "org", "repo", s)
	assert.Error(t, err)
}

func TestGetDataState_NilDB(t *testing.T) {
	ctx := context.Background()
	s := &Store{db: nil}
	_, err := s.GetDataState(ctx)
	assert.Error(t, err)
}

func TestSaveState_Upsert(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)
	since := time.Now().AddDate(0, -3, 0).UTC()

	s := &data.State{Page: 1, Since: since}
	require.NoError(t, store.SaveState(ctx, "pr", "testorg", "testrepo", s))

	// Saving again with same key should not error (upsert)
	s.Page = 10
	assert.NoError(t, store.SaveState(ctx, "pr", "testorg", "testrepo", s))
}

func TestGetState_NilDB(t *testing.T) {
	ctx := context.Background()
	s := &Store{db: nil}
	min := time.Now().AddDate(0, -6, 0).UTC()
	_, err := s.GetState(ctx, "pr", "org", "repo", min)
	assert.Error(t, err)
}

func TestSaveState_NilDB(t *testing.T) {
	ctx := context.Background()
	s := &Store{db: nil}
	st := &data.State{Page: 1, Since: time.Now()}
	err := s.SaveState(ctx, "pr", "org", "repo", st)
	assert.Error(t, err)
}

func TestSaveAndGetState_WithBackfillUntil(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)
	min := time.Now().AddDate(0, -6, 0).UTC()
	since := time.Now().AddDate(0, -3, 0).UTC()
	backfill := time.Now().AddDate(0, 0, -21).UTC()

	s := &data.State{Page: 1, Since: since, BackfillUntil: &backfill}
	require.NoError(t, store.SaveState(ctx, "pr", "testorg", "testrepo", s))

	got, err := store.GetState(ctx, "pr", "testorg", "testrepo", min)
	require.NoError(t, err)
	require.NotNil(t, got.BackfillUntil)
	assert.WithinDuration(t, backfill, *got.BackfillUntil, time.Second)
}

func TestGetBackfillUntil_NoState(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)
	got, err := store.GetBackfillUntil(ctx, "testorg", "testrepo")
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestGetBackfillUntil_NilDB(t *testing.T) {
	ctx := context.Background()
	s := &Store{db: nil}
	_, err := s.GetBackfillUntil(ctx, "org", "repo")
	assert.Error(t, err)
}

func TestSaveBackfillUntil(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)
	since := time.Now().AddDate(0, -3, 0).UTC()

	s := &data.State{Page: 1, Since: since}
	require.NoError(t, store.SaveState(ctx, "pr", "testorg", "testrepo", s))
	require.NoError(t, store.SaveState(ctx, "issue", "testorg", "testrepo", s))

	until := time.Now().AddDate(0, 0, -28).UTC()
	require.NoError(t, store.SaveBackfillUntil(ctx, "testorg", "testrepo", until))

	got, err := store.GetBackfillUntil(ctx, "testorg", "testrepo")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.WithinDuration(t, until, *got, time.Second)
}

func TestSaveBackfillUntil_NilDB(t *testing.T) {
	ctx := context.Background()
	s := &Store{db: nil}
	err := s.SaveBackfillUntil(ctx, "org", "repo", time.Now())
	assert.Error(t, err)
}
