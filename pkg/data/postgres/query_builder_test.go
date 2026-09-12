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
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestQueryBuilder_AllNil(t *testing.T) {
	qb := newQueryBuilder(1)
	qb.addOptional("e.org", nil)
	qb.addOptional("e.repo", nil)
	assert.Empty(t, qb.clauses)
	assert.Equal(t, 1, qb.paramIdx)
}

func TestQueryBuilder_AllSet(t *testing.T) {
	org := "testorg"
	repo := "testrepo"
	qb := newQueryBuilder(1)
	qb.addOptional("e.org", &org)
	qb.addOptional("e.repo", &repo)
	assert.Len(t, qb.clauses, 2)
	assert.Equal(t, "e.org = $1", qb.clauses[0])
	assert.Equal(t, "e.repo = $2", qb.clauses[1])
	assert.Equal(t, []any{"testorg", "testrepo"}, qb.args)
}

func TestQueryBuilder_Mixed(t *testing.T) {
	repo := "testrepo"
	qb := newQueryBuilder(3)
	qb.addOptional("e.org", nil)
	qb.addOptional("e.repo", &repo)
	assert.Len(t, qb.clauses, 1)
	assert.Equal(t, "e.repo = $3", qb.clauses[0])
	assert.Equal(t, []any{"testrepo"}, qb.args)
}

func TestQueryBuilder_EntityFilter(t *testing.T) {
	entity := "GOOGLE"
	qb := newQueryBuilder(1)
	qb.addOptional("d.entity", &entity)
	assert.Len(t, qb.clauses, 1)
	assert.Equal(t, "d.entity = $1", qb.clauses[0])
	assert.Equal(t, []any{"GOOGLE"}, qb.args)
}

func TestQueryBuilder_EntityNil(t *testing.T) {
	qb := newQueryBuilder(1)
	qb.addOptional("d.entity", nil)
	assert.Empty(t, qb.clauses)
}

func TestQueryBuilder_WhereClause(t *testing.T) {
	org := "testorg"
	qb := newQueryBuilder(1)
	qb.addOptional("e.org", &org)
	assert.Equal(t, " AND e.org = $1", qb.whereClause())
}

func TestQueryBuilder_WhereClauseEmpty(t *testing.T) {
	qb := newQueryBuilder(1)
	assert.Equal(t, "", qb.whereClause())
}

func TestQueryBuilder_NextParam(t *testing.T) {
	org := "testorg"
	repo := "testrepo"
	qb := newQueryBuilder(2)
	qb.addOptional("e.org", &org)
	qb.addOptional("e.repo", &repo)
	assert.Equal(t, 4, qb.nextParam())
}

func TestQueryBuilder_WhereClauseMultiple(t *testing.T) {
	org := "org1"
	repo := "repo1"
	entity := "ACME"
	qb := newQueryBuilder(2)
	qb.addOptional("e.org", &org)
	qb.addOptional("e.repo", &repo)
	qb.addOptional("d.entity", &entity)
	assert.Equal(t, " AND e.org = $2 AND e.repo = $3 AND d.entity = $4", qb.whereClause())
	assert.Equal(t, []any{"org1", "repo1", "ACME"}, qb.args)
	assert.Equal(t, 5, qb.nextParam())
}
