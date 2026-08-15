package postgres

import (
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Production's base schema_version table retains the rows from the migrations
// that 001_initial.sql squashed ("squashed from migrations 001-014"), so its
// high-water mark is 14 -- not 1. applyMigrations skips any file numbered at
// or below the current version, which means a new migration must be numbered
// above 14 to run at all.
//
// A fresh test container starts at version 0 and applies everything, so it
// cannot distinguish a correctly-numbered migration from one that production
// will silently skip. This test reconstructs the pre-migration schema and the
// real version history so that gap is covered.
var legacySchemaVersions = []int{1, 2, 3, 4, 12, 13, 14}

// legacyDDL is the base schema as it exists in production before the temporal
// type migration: every date/time column stored as TEXT.
const legacyDDL = `
CREATE TABLE devpulse_developer (
    username TEXT NOT NULL,
    full_name TEXT NOT NULL,
    email TEXT,
    avatar TEXT,
    url TEXT,
    entity TEXT,
    reputation REAL,
    reputation_updated_at TEXT,
    PRIMARY KEY (username)
);
CREATE TABLE devpulse_event (
    org TEXT NOT NULL, repo TEXT NOT NULL, username TEXT NOT NULL,
    type TEXT NOT NULL,
    date TEXT NOT NULL,
    url TEXT NOT NULL, mentions TEXT NOT NULL, labels TEXT NOT NULL,
    state TEXT, number INTEGER,
    created_at TEXT, closed_at TEXT, merged_at TEXT,
    additions INTEGER, deletions INTEGER,
    title TEXT NOT NULL DEFAULT '', changed_files INTEGER, commits INTEGER,
    PRIMARY KEY (org, repo, username, type, date)
);
CREATE INDEX idx_devpulse_event_username ON devpulse_event (username);
CREATE INDEX idx_devpulse_event_username_org_repo ON devpulse_event (username, org, repo);
CREATE TABLE devpulse_repo_meta (
    org TEXT NOT NULL, repo TEXT NOT NULL,
    stars INTEGER NOT NULL DEFAULT 0, forks INTEGER NOT NULL DEFAULT 0,
    open_issues INTEGER NOT NULL DEFAULT 0, language TEXT, license TEXT,
    archived INTEGER NOT NULL DEFAULT 0,
    updated_at TEXT NOT NULL DEFAULT '',
    last_import_at TEXT NOT NULL DEFAULT '',
    has_coc INTEGER NOT NULL DEFAULT 0, has_contributing INTEGER NOT NULL DEFAULT 0,
    has_readme INTEGER NOT NULL DEFAULT 0, has_issue_template INTEGER NOT NULL DEFAULT 0,
    has_pr_template INTEGER NOT NULL DEFAULT 0, community_health_pct INTEGER NOT NULL DEFAULT 0,
    pushed_at TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (org, repo)
);
CREATE TABLE devpulse_release (
    org TEXT NOT NULL, repo TEXT NOT NULL, tag TEXT NOT NULL, name TEXT,
    published_at TEXT, prerelease INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (org, repo, tag)
);
CREATE TABLE devpulse_container_version (
    org TEXT NOT NULL, repo TEXT NOT NULL, package TEXT NOT NULL,
    version_id INTEGER NOT NULL, tag TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    PRIMARY KEY (org, repo, package, version_id)
);
CREATE TABLE devpulse_repo_metric_history (
    org TEXT NOT NULL, repo TEXT NOT NULL,
    date TEXT NOT NULL,
    stars INTEGER NOT NULL DEFAULT 0, forks INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (org, repo, date)
);
CREATE TABLE devpulse_repo_insights (
    org TEXT NOT NULL, repo TEXT NOT NULL, insights_json TEXT NOT NULL,
    period_months INTEGER NOT NULL DEFAULT 3, model TEXT NOT NULL DEFAULT '',
    generated_at TEXT NOT NULL,
    event_count INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (org, repo)
);
`

// legacyRows seeds values in the shapes the production audit found, including
// the empty-string pushed_at that NULLIF must convert to NULL.
const legacyRows = `
INSERT INTO devpulse_developer (username, full_name, reputation_updated_at)
    VALUES ('gopher', 'Go Pher', '2026-03-01T14:22:11Z'), ('nobody', 'No Body', NULL);
INSERT INTO devpulse_event (org, repo, username, type, date, url, mentions, labels, created_at, closed_at, merged_at)
    VALUES ('o','r','gopher','pr','2026-03-01','u','','','2026-03-01T14:22:11Z','2026-03-05T09:03:00Z',NULL);
INSERT INTO devpulse_repo_meta (org, repo, updated_at, last_import_at, pushed_at)
    VALUES ('o','r','2026-03-01T14:22:11Z','2026-03-01T14:22:11Z','');
INSERT INTO devpulse_release (org, repo, tag, published_at)
    VALUES ('o','r','v1','2026-03-01T14:22:11Z');
INSERT INTO devpulse_repo_metric_history (org, repo, date, stars, forks)
    VALUES ('o','r','2026-03-01',1,2);
INSERT INTO devpulse_repo_insights (org, repo, insights_json, generated_at)
    VALUES ('o','r','{}','2026-03-01T14:22:11Z');
`

// setupLegacyDB builds a schema in the pre-migration (all TEXT) shape with
// production's schema_version history already recorded, then returns a handle
// with migrations NOT yet run.
func setupLegacyDB(t *testing.T) *sql.DB {
	t.Helper()

	if testing.Short() {
		t.Skip("skipping postgres integration test in short mode")
	}

	schema := fmt.Sprintf("legacy_%d_%d", os.Getpid(), schemaSeq.Add(1))
	_, err := sharedPool.Exec(fmt.Sprintf("CREATE SCHEMA %s", schema))
	require.NoError(t, err)

	dsn := sharedDSN
	if strings.Contains(dsn, "?") {
		dsn += "&search_path=" + schema
	} else {
		dsn += "?search_path=" + schema
	}

	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	db.SetMaxOpenConns(3)
	db.SetMaxIdleConns(2)
	require.NoError(t, db.Ping())

	t.Cleanup(func() {
		db.Close()
		sharedPool.Exec(fmt.Sprintf("DROP SCHEMA %s CASCADE", schema))
	})

	_, err = db.Exec(legacyDDL)
	require.NoError(t, err)
	_, err = db.Exec(legacyRows)
	require.NoError(t, err)

	_, err = db.Exec(`CREATE TABLE schema_version (
		version INTEGER PRIMARY KEY,
		applied_at TIMESTAMP NOT NULL DEFAULT NOW())`)
	require.NoError(t, err)
	for _, v := range legacySchemaVersions {
		_, err = db.Exec("INSERT INTO schema_version (version) VALUES ($1)", v)
		require.NoError(t, err)
	}

	return db
}

func columnType(t *testing.T, db *sql.DB, table, column string) string {
	t.Helper()
	var ty string
	err := db.QueryRow(`SELECT data_type FROM information_schema.columns
		WHERE table_name = $1 AND column_name = $2
		  AND table_schema = current_schema()`, table, column).Scan(&ty)
	require.NoError(t, err, "column %s.%s must exist", table, column)
	return ty
}

// TestMigrations_ApplyOverLegacyVersionHistory is the regression test for the
// numbering bug: with schema_version already at 14, migrations numbered at or
// below 14 are skipped and the columns silently stay TEXT while the code that
// assumes native types ships anyway.
func TestMigrations_ApplyOverLegacyVersionHistory(t *testing.T) {
	db := setupLegacyDB(t)

	// Precondition: legacy shape.
	require.Equal(t, "text", columnType(t, db, "devpulse_event", "date"))

	require.NoError(t, runMigrations(db))

	var version int
	require.NoError(t, db.QueryRow("SELECT MAX(version) FROM schema_version").Scan(&version))
	assert.Greater(t, version, 14,
		"migrations must be numbered above production's squashed high-water mark of 14")

	for _, tc := range []struct {
		table, column, want string
	}{
		{"devpulse_event", "date", "date"},
		{"devpulse_event", "created_at", "timestamp with time zone"},
		{"devpulse_event", "closed_at", "timestamp with time zone"},
		{"devpulse_event", "merged_at", "timestamp with time zone"},
		{"devpulse_repo_meta", "updated_at", "timestamp with time zone"},
		{"devpulse_repo_meta", "last_import_at", "timestamp with time zone"},
		{"devpulse_repo_meta", "pushed_at", "timestamp with time zone"},
		{"devpulse_developer", "reputation_updated_at", "timestamp with time zone"},
		{"devpulse_release", "published_at", "timestamp with time zone"},
		{"devpulse_container_version", "created_at", "timestamp with time zone"},
		{"devpulse_repo_metric_history", "date", "date"},
		{"devpulse_repo_insights", "generated_at", "timestamp with time zone"},
	} {
		assert.Equal(t, tc.want, columnType(t, db, tc.table, tc.column),
			"%s.%s must be migrated to a native temporal type", tc.table, tc.column)
	}

	// The redundant index must be gone.
	var idx int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM pg_indexes
		WHERE indexname = 'idx_devpulse_event_username'
		  AND schemaname = current_schema()`).Scan(&idx))
	assert.Zero(t, idx, "idx_devpulse_event_username must be dropped")
}

// Legacy values must survive the cast, and the empty-string sentinel must
// become NULL rather than failing the migration.
func TestMigrations_LegacyDataSurvivesCast(t *testing.T) {
	db := setupLegacyDB(t)
	require.NoError(t, runMigrations(db))

	var date, createdAt string
	require.NoError(t, db.QueryRow(`SELECT date::text,
		TO_CHAR(created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"')
		FROM devpulse_event`).Scan(&date, &createdAt))
	assert.Equal(t, "2026-03-01", date)
	assert.Equal(t, "2026-03-01T14:22:11Z", createdAt)

	var pushedAtNull bool
	require.NoError(t, db.QueryRow(
		`SELECT pushed_at IS NULL FROM devpulse_repo_meta`).Scan(&pushedAtNull))
	assert.True(t, pushedAtNull, "empty-string pushed_at must become NULL, not fail the cast")
}

// Re-running the chain must be a no-op: the guards make every migration
// idempotent, which is what lets them run against both legacy and fresh DBs.
func TestMigrations_IdempotentOverLegacy(t *testing.T) {
	db := setupLegacyDB(t)
	require.NoError(t, runMigrations(db))
	require.NoError(t, runMigrations(db), "second run must be a no-op")
	assert.Equal(t, "date", columnType(t, db, "devpulse_event", "date"))
}
