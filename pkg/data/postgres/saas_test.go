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
	"github.com/stretchr/testify/require"
)

func TestRunSaaSMigrations(t *testing.T) {
	store := setupTestDB(t)
	db := store.DB()

	err := RunSaaSMigrations(db)
	require.NoError(t, err)

	// Verify tenant table exists
	var exists bool
	err = db.QueryRow(`SELECT EXISTS (
		SELECT 1 FROM information_schema.tables
		WHERE table_name = 'devpulse_tenant' AND table_schema = current_schema()
	)`).Scan(&exists)
	require.NoError(t, err)
	assert.True(t, exists, "tenant table should exist")

	// Verify session table exists
	err = db.QueryRow(`SELECT EXISTS (
		SELECT 1 FROM information_schema.tables
		WHERE table_name = 'devpulse_session' AND table_schema = current_schema()
	)`).Scan(&exists)
	require.NoError(t, err)
	assert.True(t, exists, "session table should exist")

	// Verify idempotency
	err = RunSaaSMigrations(db)
	require.NoError(t, err)
}
