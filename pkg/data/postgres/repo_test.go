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

func TestGetRepoLike(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)
	seedTestData(t, store)
	items, err := store.GetRepoLike(ctx, "test", 10)
	require.NoError(t, err)
	assert.NotEmpty(t, items)
	assert.Contains(t, items[0].Value, "testorg/testrepo")
}

func TestGetRepoLike_EmptyQuery(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)
	_, err := store.GetRepoLike(ctx, "", 10)
	assert.Error(t, err)
}

func TestGetRepoLike_NilDB(t *testing.T) {
	ctx := context.Background()
	s := &Store{db: nil}
	_, err := s.GetRepoLike(ctx, "test", 10)
	assert.Error(t, err)
}

func TestGetRepoLike_NoResults(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)
	seedTestData(t, store)
	items, err := store.GetRepoLike(ctx, "nonexistent", 10)
	require.NoError(t, err)
	assert.Empty(t, items)
}
