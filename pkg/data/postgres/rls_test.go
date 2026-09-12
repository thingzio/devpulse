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
	"database/sql"
	"fmt"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// replaceUserInDSN swaps the user and password in a postgres:// DSN.
func replaceUserInDSN(dsn, user, pass string) string {
	u, err := url.Parse(dsn)
	if err != nil {
		return dsn
	}
	u.User = url.UserPassword(user, pass)
	return u.String()
}

// setupTestDBWithSaaS creates an isolated test schema where a non-superuser
// role owns all tables and has both base and SaaS migrations applied.
// This mirrors production where the Cloud SQL IAM user is the table owner
// but is NOT a superuser. Returns the app DB pool (non-superuser, table owner).
// Seeding and querying both go through this pool.
func setupTestDBWithSaaS(t *testing.T) *sql.DB {
	t.Helper()

	if testing.Short() {
		t.Skip("skipping postgres integration test in short mode")
	}

	ctx := context.Background()
	schema := fmt.Sprintf("rls_%d", schemaSeq.Add(1))
	appRole := schema + "_app"
	appPass := "testpass"

	// Create schema and non-superuser role using the shared superuser pool.
	_, err := sharedPool.ExecContext(ctx, fmt.Sprintf("CREATE SCHEMA %s", schema))
	require.NoError(t, err)
	_, err = sharedPool.ExecContext(ctx, fmt.Sprintf("CREATE ROLE %s LOGIN PASSWORD '%s'", appRole, appPass))
	require.NoError(t, err)
	_, err = sharedPool.ExecContext(ctx, fmt.Sprintf("GRANT ALL ON SCHEMA %s TO %s", schema, appRole))
	require.NoError(t, err)

	t.Cleanup(func() {
		sharedPool.ExecContext(ctx, fmt.Sprintf("DROP SCHEMA %s CASCADE", schema))
		sharedPool.ExecContext(ctx, fmt.Sprintf("DROP ROLE IF EXISTS %s", appRole))
	})

	// Connect as the non-superuser and run migrations — making it the table owner.
	appDSN := replaceUserInDSN(sharedDSN, appRole, appPass) + "&search_path=" + schema
	appPool, err := sql.Open("postgres", appDSN)
	require.NoError(t, err)
	appPool.SetMaxOpenConns(3)
	appPool.SetMaxIdleConns(2)
	require.NoError(t, appPool.Ping())
	require.NoError(t, runMigrations(appPool))
	require.NoError(t, RunSaaSMigrations(appPool))

	t.Cleanup(func() { appPool.Close() })

	return appPool
}

// seedTwoTenants creates two tenants, each with one repo and one event.
// Returns (tenantA_id, tenantB_id).
func seedTwoTenants(t *testing.T, db *sql.DB) (string, string) {
	t.Helper()
	ctx := context.Background()

	// Create tenants.
	var tidA, tidB string
	err := db.QueryRowContext(ctx,
		`INSERT INTO devpulse_tenant(github_id, username, email, tos_accepted_at) VALUES (1001, 'alice', 'alice@test.com', NOW()) RETURNING id`,
	).Scan(&tidA)
	require.NoError(t, err)

	err = db.QueryRowContext(ctx,
		`INSERT INTO devpulse_tenant(github_id, username, email, tos_accepted_at) VALUES (1002, 'bob', 'bob@test.com', NOW()) RETURNING id`,
	).Scan(&tidB)
	require.NoError(t, err)

	// Add repos for each tenant.
	_, err = db.ExecContext(ctx,
		`INSERT INTO devpulse_tenant_repo(tenant_id, org, repo) VALUES ($1, 'orgA', 'repoA')`, tidA)
	require.NoError(t, err)

	_, err = db.ExecContext(ctx,
		`INSERT INTO devpulse_tenant_repo(tenant_id, org, repo) VALUES ($1, 'orgB', 'repoB')`, tidB)
	require.NoError(t, err)

	// Insert a developer shared across repos.
	_, err = db.ExecContext(ctx,
		`INSERT INTO devpulse_developer(username, full_name) VALUES ('dev1', 'Developer One')`)
	require.NoError(t, err)

	// Insert events for each repo.
	_, err = db.ExecContext(ctx,
		`INSERT INTO devpulse_event(org, repo, username, type, date, url, mentions, labels) VALUES ('orgA', 'repoA', 'dev1', 'push', NOW(), '', '', '')`)
	require.NoError(t, err)

	_, err = db.ExecContext(ctx,
		`INSERT INTO devpulse_event(org, repo, username, type, date, url, mentions, labels) VALUES ('orgB', 'repoB', 'dev1', 'push', NOW(), '', '', '')`)
	require.NoError(t, err)

	return tidA, tidB
}

func TestRLS_ScopedConnectionSeesOnlyOwnTenantEvents(t *testing.T) {
	appDB := setupTestDBWithSaaS(t)
	tidA, _ := seedTwoTenants(t, appDB)
	ctx := context.Background()

	// Acquire a dedicated connection as non-superuser and scope to tenant A.
	conn, err := appDB.Conn(ctx)
	require.NoError(t, err)
	defer conn.Close()

	_, err = conn.ExecContext(ctx, "SELECT set_config('app.tenant_id', $1, false)", tidA)
	require.NoError(t, err)

	// Tenant A should see only orgA/repoA events.
	var count int
	err = conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM devpulse_event").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count, "tenant A should see exactly 1 event")

	var org string
	err = conn.QueryRowContext(ctx, "SELECT org FROM devpulse_event").Scan(&org)
	require.NoError(t, err)
	assert.Equal(t, "orgA", org, "tenant A should only see orgA events")
}

func TestRLS_ScopedConnectionSeesOnlyOwnTenantRepos(t *testing.T) {
	appDB := setupTestDBWithSaaS(t)
	tidA, _ := seedTwoTenants(t, appDB)
	ctx := context.Background()

	conn, err := appDB.Conn(ctx)
	require.NoError(t, err)
	defer conn.Close()

	_, err = conn.ExecContext(ctx, "SELECT set_config('app.tenant_id', $1, false)", tidA)
	require.NoError(t, err)

	var count int
	err = conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM devpulse_tenant_repo").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count, "tenant A should see exactly 1 repo")

	var repo string
	err = conn.QueryRowContext(ctx, "SELECT repo FROM devpulse_tenant_repo").Scan(&repo)
	require.NoError(t, err)
	assert.Equal(t, "repoA", repo)
}

func TestRLS_CrossTenantAccessBlocked(t *testing.T) {
	appDB := setupTestDBWithSaaS(t)
	tidA, _ := seedTwoTenants(t, appDB)
	ctx := context.Background()

	// Scope to tenant A and try to see tenant B's data.
	conn, err := appDB.Conn(ctx)
	require.NoError(t, err)
	defer conn.Close()

	_, err = conn.ExecContext(ctx, "SELECT set_config('app.tenant_id', $1, false)", tidA)
	require.NoError(t, err)

	// Tenant A must not see orgB events.
	var count int
	err = conn.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM devpulse_event WHERE org = 'orgB'").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 0, count, "tenant A must not see tenant B events")

	// Tenant A must not see tenant B repos.
	err = conn.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM devpulse_tenant_repo WHERE repo = 'repoB'").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 0, count, "tenant A must not see tenant B repos")
}

func TestRLS_UnscopedConnectionSeesAllData(t *testing.T) {
	appDB := setupTestDBWithSaaS(t)
	seedTwoTenants(t, appDB)
	ctx := context.Background()

	// No set_config — simulates importer/admin behavior.
	var count int
	err := appDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM devpulse_event").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 2, count, "unscoped connection should see all events")

	err = appDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM devpulse_tenant_repo").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 2, count, "unscoped connection should see all repos")
}

func TestRLS_UnscopedConnectionCanInsert(t *testing.T) {
	appDB := setupTestDBWithSaaS(t)
	seedTwoTenants(t, appDB)
	ctx := context.Background()

	// Importer inserts events without set_config.
	_, err := appDB.ExecContext(ctx,
		`INSERT INTO devpulse_event(org, repo, username, type, date, url, mentions, labels) VALUES ('orgA', 'repoA', 'dev1', 'issue', NOW(), '', '', '')`)
	require.NoError(t, err, "unscoped insert into event should succeed")

	var count int
	err = appDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM devpulse_event WHERE type = 'issue'").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count)
}

func TestRLS_ForceRowLevelSecurityEnabled(t *testing.T) {
	db := setupTestDBWithSaaS(t)
	ctx := context.Background()

	tables := []string{
		"devpulse_event", "devpulse_developer", "devpulse_repo_meta", "devpulse_release", "devpulse_release_asset",
		"devpulse_container_version", "devpulse_repo_metric_history", "devpulse_repo_insights",
		"devpulse_tenant_repo", "devpulse_tenant_member", "devpulse_github_app_installation", "devpulse_session",
	}

	for _, table := range tables {
		var rlsEnabled, rlsForced bool
		err := db.QueryRowContext(ctx,
			"SELECT relrowsecurity, relforcerowsecurity FROM pg_class WHERE relname = $1", table,
		).Scan(&rlsEnabled, &rlsForced)
		require.NoError(t, err, "querying pg_class for %s", table)
		assert.True(t, rlsEnabled, "%s should have RLS enabled", table)
		assert.True(t, rlsForced, "%s should have FORCE RLS", table)
	}
}

// TestAuthFlow_NewUser_WithCleanConnection tests the exact AuthenticateUser
// code path: dedicated connection → clear scope → upsert tenant → insert
// session. This must succeed for new users.
func TestAuthFlow_NewUser_WithCleanConnection(t *testing.T) {
	appDB := setupTestDBWithSaaS(t)
	ctx := context.Background()

	conn, err := appDB.Conn(ctx)
	require.NoError(t, err)
	defer conn.Close()

	_, err = conn.ExecContext(ctx, "SELECT set_config('app.tenant_id', '', false)")
	require.NoError(t, err)

	var tenantID string
	err = conn.QueryRowContext(ctx, `
		INSERT INTO devpulse_tenant (github_id, username, email, avatar_url, name, company, location, bio)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (github_id) DO UPDATE SET username = EXCLUDED.username, updated_at = NOW()
		RETURNING id`, int64(999999), "newuser", "new@test.com", "", "", "", "", "",
	).Scan(&tenantID)
	require.NoError(t, err, "upsert must succeed")
	require.NotEmpty(t, tenantID, "tenant ID must not be empty")

	_, err = conn.ExecContext(ctx, `INSERT INTO devpulse_session (id, tenant_id, expires_at)
		VALUES ($1, $2, NOW() + $3::interval)`, "test-session-hash-clean", tenantID, "24 hours")
	require.NoError(t, err, "session INSERT must succeed on same connection as upsert")
}

// TestAuthFlow_NewUser_DirtyPoolConnection tests auth when the connection from
// the pool previously had app.tenant_id set to another tenant. This simulates
// a pooled connection reused after a scoped data API request.
func TestAuthFlow_NewUser_DirtyPoolConnection(t *testing.T) {
	appDB := setupTestDBWithSaaS(t)
	ctx := context.Background()

	// Create an existing tenant to "dirty" the pool.
	var existingTID string
	err := appDB.QueryRowContext(ctx,
		`INSERT INTO devpulse_tenant (github_id, username, email) VALUES (3001, 'existing', 'e@test.com') RETURNING id`,
	).Scan(&existingTID)
	require.NoError(t, err)

	// Dirty a pool connection: set app.tenant_id and return to pool WITHOUT clearing.
	dirtyConn, err := appDB.Conn(ctx)
	require.NoError(t, err)
	_, err = dirtyConn.ExecContext(ctx, "SELECT set_config('app.tenant_id', $1, false)", existingTID)
	require.NoError(t, err)
	dirtyConn.Close() // returns to pool still dirty

	// Now run AuthenticateUser flow — may get the dirty connection.
	conn, err := appDB.Conn(ctx)
	require.NoError(t, err)
	defer conn.Close()

	_, err = conn.ExecContext(ctx, "SELECT set_config('app.tenant_id', '', false)")
	require.NoError(t, err)

	var tenantID string
	err = conn.QueryRowContext(ctx, `
		INSERT INTO devpulse_tenant (github_id, username, email, avatar_url, name, company, location, bio)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (github_id) DO UPDATE SET username = EXCLUDED.username, updated_at = NOW()
		RETURNING id`, int64(999998), "brandnew", "brand@test.com", "", "", "", "", "",
	).Scan(&tenantID)
	require.NoError(t, err)

	_, err = conn.ExecContext(ctx, `INSERT INTO devpulse_session (id, tenant_id, expires_at)
		VALUES ($1, $2, NOW() + $3::interval)`, "test-session-hash-dirty", tenantID, "24 hours")
	require.NoError(t, err, "session INSERT must succeed even on previously dirty connection")
}

// TestAuthFlow_FKTargetIntegrity verifies that the session FK constraint
// references devpulse_tenant (not another table). This catches the root cause
// of the production FK violation where the constraint referenced devtrace_tenant
// after table renames.
func TestAuthFlow_FKTargetIntegrity(t *testing.T) {
	appDB := setupTestDBWithSaaS(t)
	ctx := context.Background()

	var fkTarget string
	err := appDB.QueryRowContext(ctx, `
		SELECT confrelid::regclass
		FROM pg_constraint
		WHERE conrelid::regclass::text = 'devpulse_session'
		AND contype = 'f'
		AND (SELECT attname FROM pg_attribute WHERE attrelid = conrelid AND attnum = conkey[1]) = 'tenant_id'`,
	).Scan(&fkTarget)
	require.NoError(t, err, "must find FK constraint on devpulse_session.tenant_id")
	assert.Equal(t, "devpulse_tenant", fkTarget,
		"session FK must reference devpulse_tenant, got %s", fkTarget)
}

// TestAuthFlow_StaleScope_ErrorType verifies what error PostgreSQL returns when
// a session INSERT runs with a stale app.tenant_id (NOT cleared). This reveals
// whether the production FK-constraint error (23503) is actually an RLS issue.
func TestAuthFlow_StaleScope_ErrorType(t *testing.T) {
	appDB := setupTestDBWithSaaS(t)
	ctx := context.Background()

	// Create tenant A and scope a connection to it.
	var tidA string
	err := appDB.QueryRowContext(ctx,
		`INSERT INTO devpulse_tenant (github_id, username, email) VALUES (4001, 'tenantA', 'a@test.com') RETURNING id`,
	).Scan(&tidA)
	require.NoError(t, err)

	conn, err := appDB.Conn(ctx)
	require.NoError(t, err)
	defer conn.Close()

	_, err = conn.ExecContext(ctx, "SELECT set_config('app.tenant_id', $1, false)", tidA)
	require.NoError(t, err)

	// Create tenant B — devpulse_tenant has NO RLS, so this works.
	var tidB string
	err = conn.QueryRowContext(ctx, `
		INSERT INTO devpulse_tenant (github_id, username, email, avatar_url, name, company, location, bio)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (github_id) DO UPDATE SET username = EXCLUDED.username, updated_at = NOW()
		RETURNING id`, int64(4002), "tenantB", "b@test.com", "", "", "", "", "",
	).Scan(&tidB)
	require.NoError(t, err)

	// Now insert session for tenant B WITHOUT clearing app.tenant_id (still = tidA).
	// The bypass policy (app.tenant_id = '') is false.
	// The direct policy (tenant_id = tidA) is false because new row has tidB.
	// RLS should block this INSERT. What error do we get?
	_, err = conn.ExecContext(ctx, `INSERT INTO devpulse_session (id, tenant_id, expires_at)
		VALUES ($1, $2, NOW() + $3::interval)`, "stale-scope-session", tidB, "24 hours")
	require.Error(t, err, "session INSERT with stale scope must fail")
	t.Logf("Error with stale scope (tenant B session while scoped to A): %v", err)
}

func TestRLS_ScopedConnectionDeveloperIsolation(t *testing.T) {
	appDB := setupTestDBWithSaaS(t)
	tidA, tidB := seedTwoTenants(t, appDB)
	ctx := context.Background()

	// Add a second developer only referenced in orgB events.
	_, err := appDB.ExecContext(ctx,
		`INSERT INTO devpulse_developer(username, full_name) VALUES ('dev2', 'Developer Two')`)
	require.NoError(t, err)
	_, err = appDB.ExecContext(ctx,
		`INSERT INTO devpulse_event(org, repo, username, type, date, url, mentions, labels) VALUES ('orgB', 'repoB', 'dev2', 'pr', NOW(), '', '', '')`)
	require.NoError(t, err)

	// Scope to tenant A — should only see dev1 (via orgA events).
	connA, err := appDB.Conn(ctx)
	require.NoError(t, err)
	defer connA.Close()
	_, err = connA.ExecContext(ctx, "SELECT set_config('app.tenant_id', $1, false)", tidA)
	require.NoError(t, err)

	var countA int
	err = connA.QueryRowContext(ctx, "SELECT COUNT(*) FROM devpulse_developer").Scan(&countA)
	require.NoError(t, err)
	assert.Equal(t, 1, countA, "tenant A should see only dev1")

	// Scope to tenant B — should see dev1 and dev2 (both have orgB events).
	connB, err := appDB.Conn(ctx)
	require.NoError(t, err)
	defer connB.Close()
	_, err = connB.ExecContext(ctx, "SELECT set_config('app.tenant_id', $1, false)", tidB)
	require.NoError(t, err)

	var countB int
	err = connB.QueryRowContext(ctx, "SELECT COUNT(*) FROM devpulse_developer").Scan(&countB)
	require.NoError(t, err)
	assert.Equal(t, 2, countB, "tenant B should see dev1 and dev2")
}
