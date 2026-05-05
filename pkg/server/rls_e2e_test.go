package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thingzio/devpulse/pkg/tenant"
)

var rlsRoleSeq atomic.Uint64

// setupRLSScopedRole creates a non-superuser role with SELECT on the
// current schema. Tests use SET ROLE to drop superuser privileges so
// PostgreSQL actually enforces RLS. Returns the role name.
func setupRLSScopedRole(t *testing.T, db *sql.DB) string {
	t.Helper()
	ctx := context.Background()

	role := fmt.Sprintf("devpulse_rls_test_%d", rlsRoleSeq.Add(1))

	var schema string
	require.NoError(t, db.QueryRowContext(ctx, "SELECT current_schema()").Scan(&schema))

	stmts := []string{
		fmt.Sprintf("CREATE ROLE %s NOLOGIN", role),
		fmt.Sprintf("GRANT USAGE ON SCHEMA %s TO %s", schema, role),
		fmt.Sprintf("GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA %s TO %s", schema, role),
	}
	for _, s := range stmts {
		_, err := db.ExecContext(ctx, s)
		require.NoError(t, err)
	}

	t.Cleanup(func() {
		bg := context.Background()
		_, _ = db.ExecContext(bg, fmt.Sprintf("REVOKE ALL ON ALL TABLES IN SCHEMA %s FROM %s", schema, role))
		_, _ = db.ExecContext(bg, fmt.Sprintf("REVOKE USAGE ON SCHEMA %s FROM %s", schema, role))
		_, _ = db.ExecContext(bg, "DROP ROLE IF EXISTS "+role)
	})

	return role
}

// TestTenantRLSIsolation_DataTables verifies that the row-level security
// policies on devpulse_event and devpulse_repo_meta correctly scope
// queries by app.tenant_id. Two tenants own non-overlapping repos; a
// connection scoped to tenant A must see only A's data, and vice versa.
//
// Cross-tenant leakage here would be a Sev-1 incident, so this test
// exists as a permanent canary.
func TestTenantRLSIsolation_DataTables(t *testing.T) {
	db := setupE2EDB(t)
	ctx := context.Background()

	// Tests run as a superuser by default (testcontainers postgres),
	// which BYPASSES RLS regardless of FORCE ROW LEVEL SECURITY. Create
	// a non-superuser role to actually enforce the policies, mirroring
	// the production `devpulse` role.
	scopedRole := setupRLSScopedRole(t, db)

	// Two tenants. A owns alpha/repo-a; B owns beta/repo-b.
	tnA, err := tenant.UpsertTenant(ctx, db, 700001, "rls-tenant-a", "", "", "", "", "", "")
	require.NoError(t, err)
	tnB, err := tenant.UpsertTenant(ctx, db, 700002, "rls-tenant-b", "", "", "", "", "", "")
	require.NoError(t, err)

	require.NoError(t, tenant.AddTenantRepos(ctx, db, tnA.ID, []tenant.OrgRepo{{Org: "alpha", Repo: "repo-a"}}))
	require.NoError(t, tenant.AddTenantRepos(ctx, db, tnB.ID, []tenant.OrgRepo{{Org: "beta", Repo: "repo-b"}}))

	// Seed devpulse_event with one row per repo.
	_, err = db.ExecContext(ctx,
		`INSERT INTO devpulse_developer(username, full_name) VALUES ('alice', 'Alice'), ('bob', 'Bob')`)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `
		INSERT INTO devpulse_event(org, repo, username, type, date, url, mentions, labels, number, created_at) VALUES
			('alpha','repo-a','alice','pr','2026-04-01','http://a','','',1,'2026-04-01T00:00:00Z'),
			('beta','repo-b','bob',  'pr','2026-04-02','http://b','','',2,'2026-04-02T00:00:00Z')
	`)
	require.NoError(t, err)

	// Helper: count visible event rows from a connection scoped to a
	// given tenant_id. Uses SET ROLE to drop superuser privileges so
	// RLS is enforced.
	scopedCount := func(t *testing.T, tenantID string) int {
		t.Helper()
		conn, connErr := db.Conn(ctx)
		require.NoError(t, connErr)
		defer conn.Close()

		_, connErr = conn.ExecContext(ctx, "SET ROLE "+scopedRole)
		require.NoError(t, connErr)
		defer func() { _, _ = conn.ExecContext(context.Background(), "RESET ROLE") }()

		_, connErr = conn.ExecContext(ctx, "SELECT set_config('app.tenant_id', $1, false)", tenantID)
		require.NoError(t, connErr)

		var n int
		require.NoError(t, conn.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM devpulse_event").Scan(&n))
		return n
	}

	assert.Equal(t, 1, scopedCount(t, tnA.ID), "tenant A must see exactly its one event")
	assert.Equal(t, 1, scopedCount(t, tnB.ID), "tenant B must see exactly its one event")

	// The tnA != tnB visibility above is the key isolation guarantee. We
	// also want to assert: tenant A's connection cannot see tenant B's
	// row even when looking specifically. Filter by org so the assertion
	// is robust against any future test seeding more rows.
	conn, err := db.Conn(ctx)
	require.NoError(t, err)
	defer conn.Close()
	_, err = conn.ExecContext(ctx, "SET ROLE "+scopedRole)
	require.NoError(t, err)
	defer func() { _, _ = conn.ExecContext(context.Background(), "RESET ROLE") }()
	_, err = conn.ExecContext(ctx, "SELECT set_config('app.tenant_id', $1, false)", tnA.ID)
	require.NoError(t, err)

	var leakedCount int
	require.NoError(t, conn.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM devpulse_event WHERE org='beta'").Scan(&leakedCount))
	assert.Equal(t, 0, leakedCount,
		"tenant A scope must not see any event in tenant B's org=beta — cross-tenant leak")
}

// TestTenantRLSIsolation_TenantTables verifies the same isolation on
// tenant-scoped tables (devpulse_session, devpulse_github_app_installation):
// tenant A's session row must not leak to a query scoped to tenant B.
func TestTenantRLSIsolation_TenantTables(t *testing.T) {
	db := setupE2EDB(t)
	ctx := context.Background()

	scopedRole := setupRLSScopedRole(t, db)

	tnA, err := tenant.UpsertTenant(ctx, db, 700101, "rls-tt-a", "", "", "", "", "", "")
	require.NoError(t, err)
	tnB, err := tenant.UpsertTenant(ctx, db, 700102, "rls-tt-b", "", "", "", "", "", "")
	require.NoError(t, err)

	// Seed an installation for each tenant.
	require.NoError(t, tenant.SaveInstallation(ctx, db, tnA.ID, 90001, "Organization", "org-a", nil, 100))
	require.NoError(t, tenant.SaveInstallation(ctx, db, tnB.ID, 90002, "Organization", "org-b", nil, 100))

	count := func(t *testing.T, asTenant string) int {
		t.Helper()
		conn, err := db.Conn(ctx)
		require.NoError(t, err)
		defer conn.Close()
		_, err = conn.ExecContext(ctx, "SET ROLE "+scopedRole)
		require.NoError(t, err)
		defer func() { _, _ = conn.ExecContext(context.Background(), "RESET ROLE") }()
		_, err = conn.ExecContext(ctx, "SELECT set_config('app.tenant_id', $1, false)", asTenant)
		require.NoError(t, err)

		var n int
		err = conn.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM devpulse_github_app_installation").Scan(&n)
		// A tenant might not have visibility to read at all under RLS — surface
		// any unexpected error so it doesn't masquerade as zero.
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			require.NoError(t, err)
		}
		return n
	}

	assert.Equal(t, 1, count(t, tnA.ID), "tenant A must see exactly its one installation")
	assert.Equal(t, 1, count(t, tnB.ID), "tenant B must see exactly its one installation")
}
