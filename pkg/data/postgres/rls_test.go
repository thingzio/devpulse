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
