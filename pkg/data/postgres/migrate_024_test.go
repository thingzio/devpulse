package postgres

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMigration024_DedupesPRRows replays the production failure mode against
// a freshly-seeded DB: insert seven rows for one PR (six stale 'open'
// snapshots + one terminal 'closed' row), execute migration 024 again, and
// verify a single row remains with the closed state and date set to
// created_at::date.
//
// setupTestDB applies migration 024 on an empty schema; we re-execute the
// SQL to exercise the dedup path on the rows we just seeded.
func TestMigration024_DedupesPRRows(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)

	migrationSQL, err := saasMigrationsFS.ReadFile("sql/migrations_saas/024_dedupe_events.sql")
	require.NoError(t, err)

	_, err = store.db.ExecContext(ctx,
		`INSERT INTO devpulse_developer(username, full_name) VALUES ('alice', 'Alice')`)
	require.NoError(t, err)

	// Seed PR #7544 with the exact pattern observed in production:
	// six 'open' snapshots dated by UpdatedAt, one terminal 'closed' row.
	createdAt := "2026-03-20T11:19:14Z"
	openRows := []string{
		"2026-04-01", "2026-04-06", "2026-04-07",
		"2026-04-11", "2026-04-13", "2026-04-15",
	}
	for _, d := range openRows {
		_, err = store.db.ExecContext(ctx,
			`INSERT INTO devpulse_event(org, repo, username, type, date, url, mentions, labels, state, number, created_at)
			 VALUES ('ai-dynamo', 'dynamo', 'alice', 'pr', $1, 'http://x', '', '', 'open', 7544, $2)`,
			d, createdAt)
		require.NoError(t, err)
	}
	closedAt := "2026-04-16T07:41:22Z"
	_, err = store.db.ExecContext(ctx,
		`INSERT INTO devpulse_event(org, repo, username, type, date, url, mentions, labels, state, number, created_at, closed_at)
		 VALUES ('ai-dynamo', 'dynamo', 'alice', 'pr', '2026-04-16', 'http://x', '', '', 'closed', 7544, $1, $2)`,
		createdAt, closedAt)
	require.NoError(t, err)

	// Confirm the pre-migration state: 7 rows for one PR, mixed states.
	var preCount int
	require.NoError(t, store.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM devpulse_event WHERE org='ai-dynamo' AND repo='dynamo' AND number=7544`,
	).Scan(&preCount))
	require.Equal(t, 7, preCount)

	// Migration must execute inside a transaction (uses ON COMMIT DROP).
	tx, err := store.db.BeginTx(ctx, nil)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, string(migrationSQL))
	require.NoError(t, err)
	require.NoError(t, tx.Commit())

	// Post-migration: exactly one row, closed state, date := created_at::date.
	var rows int
	require.NoError(t, store.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM devpulse_event WHERE org='ai-dynamo' AND repo='dynamo' AND number=7544`,
	).Scan(&rows))
	assert.Equal(t, 1, rows, "PR #7544 must collapse to a single row")

	var state, date, mergedAt, closedAtCol string
	err = store.db.QueryRowContext(ctx,
		`SELECT state, date::text,
		        COALESCE(TO_CHAR(merged_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'), ''),
		        COALESCE(TO_CHAR(closed_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'), '')
		 FROM devpulse_event WHERE org='ai-dynamo' AND repo='dynamo' AND number=7544`,
	).Scan(&state, &date, &mergedAt, &closedAtCol)
	require.NoError(t, err)
	assert.Equal(t, "closed", state, "winner must be the terminal-state row")
	assert.Equal(t, "2026-03-20", date, "date must be reset to created_at::date")
	assert.Equal(t, closedAt, closedAtCol, "closed_at must survive on the kept row")
}

// TestMigration025_WipesForksOnly seeds a representative mix of event
// types — fork rows that should be wiped and PR/issue/comment/review
// rows that must be preserved — then re-executes migration 025 and
// asserts the expected delete/keep behavior.
//
// Production sample (post-deploy): 97,492 fork rows wiped, 0 rows of
// other types touched; this test mirrors the same invariants in CI.
func TestMigration025_WipesForksOnly(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)

	migrationSQL, err := saasMigrationsFS.ReadFile("sql/migrations_saas/025_dedupe_forks.sql")
	require.NoError(t, err)

	_, err = store.db.ExecContext(ctx,
		`INSERT INTO devpulse_developer(username, full_name) VALUES ('alice', 'Alice')`)
	require.NoError(t, err)

	// Seed: 3 fork rows (must be wiped), one each of pr / issue /
	// issue_comment / pr_review (must survive).
	_, err = store.db.ExecContext(ctx, `
		INSERT INTO devpulse_event(org, repo, username, type, date, url, mentions, labels, number, created_at) VALUES
			('o','r','alice','fork',         '2026-04-01','http://f1','','',NULL,'2026-04-01T00:00:00Z'),
			('o','r','alice','fork',         '2026-04-02','http://f2','','',NULL,'2026-04-02T00:00:00Z'),
			('o','r','alice','fork',         '2026-04-03','http://f3','','',NULL,'2026-04-03T00:00:00Z'),
			('o','r','alice','pr',           '2026-04-04','http://p1','','',1,   '2026-04-04T00:00:00Z'),
			('o','r','alice','issue',        '2026-04-05','http://i1','','',2,   '2026-04-05T00:00:00Z'),
			('o','r','alice','issue_comment','2026-04-06','http://c1','','',3,   '2026-04-06T00:00:00Z'),
			('o','r','alice','pr_review',    '2026-04-07','http://r1','','',4,   '2026-04-07T00:00:00Z')
	`)
	require.NoError(t, err)

	// Re-execute the migration on the seeded data.
	tx, err := store.db.BeginTx(ctx, nil)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, string(migrationSQL))
	require.NoError(t, err)
	require.NoError(t, tx.Commit())

	var forks, prs, issues, comments, reviews int
	row := store.db.QueryRowContext(ctx, `
		SELECT
			COUNT(*) FILTER (WHERE type='fork'),
			COUNT(*) FILTER (WHERE type='pr'),
			COUNT(*) FILTER (WHERE type='issue'),
			COUNT(*) FILTER (WHERE type='issue_comment'),
			COUNT(*) FILTER (WHERE type='pr_review')
		FROM devpulse_event WHERE org='o' AND repo='r'`)
	require.NoError(t, row.Scan(&forks, &prs, &issues, &comments, &reviews))

	assert.Equal(t, 0, forks, "all fork rows must be wiped")
	assert.Equal(t, 1, prs, "PR rows must survive")
	assert.Equal(t, 1, issues, "issue rows must survive")
	assert.Equal(t, 1, comments, "issue_comment rows must survive")
	assert.Equal(t, 1, reviews, "pr_review rows must survive")
}

// TestMigration024_PreservesOtherEventTypes guards against scope creep —
// the migration must only touch type IN ('pr','issue') with non-null number.
// Comments, reviews, and forks must pass through untouched.
func TestMigration024_PreservesOtherEventTypes(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)

	migrationSQL, err := saasMigrationsFS.ReadFile("sql/migrations_saas/024_dedupe_events.sql")
	require.NoError(t, err)

	_, err = store.db.ExecContext(ctx,
		`INSERT INTO devpulse_developer(username, full_name) VALUES ('alice', 'Alice')`)
	require.NoError(t, err)

	// Seed one row of each unaffected type.
	_, err = store.db.ExecContext(ctx, `
		INSERT INTO devpulse_event(org, repo, username, type, date, url, mentions, labels, number, created_at) VALUES
			('o','r','alice','issue_comment','2026-04-10','http://c1','','',NULL,'2026-04-10T00:00:00Z'),
			('o','r','alice','pr_review','2026-04-11','http://r1','','',1,'2026-04-11T00:00:00Z'),
			('o','r','alice','fork','2026-04-12','http://f1','','',NULL,'2026-04-12T00:00:00Z')`)
	require.NoError(t, err)

	tx, err := store.db.BeginTx(ctx, nil)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, string(migrationSQL))
	require.NoError(t, err)
	require.NoError(t, tx.Commit())

	for _, typ := range []string{"issue_comment", "pr_review", "fork"} {
		var n int
		err = store.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM devpulse_event WHERE org='o' AND repo='r' AND type=$1`, typ,
		).Scan(&n)
		require.NoError(t, err)
		assert.Equal(t, 1, n, "%s row must survive the migration untouched", typ)
	}
}
