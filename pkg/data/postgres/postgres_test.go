package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// sharedDSN holds the connection string for the single shared postgres container.
var sharedDSN string

// schemaSeq generates unique schema names across tests.
var schemaSeq atomic.Uint64

// sharedPool is a single connection pool for schema create/drop operations.
// Prevents each test from opening its own unlimited pool against the container.
var sharedPool *sql.DB

func TestMain(m *testing.M) {
	// Cannot call testing.Short() before flag.Parse(); check the flag directly.
	for _, arg := range os.Args[1:] {
		if arg == "-test.short" || arg == "-test.short=true" {
			os.Exit(m.Run())
		}
	}

	// Use DEVPULSE_TEST_DSN if set (CI service container), else start testcontainer.
	var cleanup func()
	if dsn := os.Getenv("DEVPULSE_TEST_DSN"); dsn != "" {
		sharedDSN = dsn
		cleanup = func() {}
	} else {
		ctx := context.Background()
		container, err := tcpostgres.Run(ctx, "postgres:17-alpine",
			tcpostgres.WithDatabase("devpulse_test"),
			tcpostgres.WithUsername("test"),
			tcpostgres.WithPassword("test"),
			testcontainers.WithWaitStrategy(
				wait.ForListeningPort("5432/tcp").WithStartupTimeout(60*time.Second),
			),
		)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to start postgres container: %v\n", err)
			os.Exit(1)
		}
		dsn, err := container.ConnectionString(ctx, "sslmode=disable")
		if err != nil {
			container.Terminate(ctx)
			fmt.Fprintf(os.Stderr, "failed to get connection string: %v\n", err)
			os.Exit(1)
		}
		sharedDSN = dsn
		cleanup = func() { container.Terminate(ctx) }
	}

	var err error
	sharedPool, err = sql.Open("postgres", sharedDSN)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to open shared pool: %v\n", err)
		os.Exit(1)
	}
	sharedPool.SetMaxOpenConns(5)
	sharedPool.SetMaxIdleConns(2)

	code := m.Run()
	sharedPool.Close()
	cleanup()
	os.Exit(code)
}

func setupTestDB(t *testing.T) *Store {
	t.Helper()

	if testing.Short() {
		t.Skip("skipping postgres integration test in short mode")
	}

	// PID prefix prevents schema-name collisions when CI runs pkg/data/postgres,
	// pkg/server, and pkg/tenant in parallel against the shared DEVPULSE_TEST_DSN
	// — each package's counter is process-local and would otherwise produce
	// overlapping "test_N" names across the three test binaries.
	schema := fmt.Sprintf("test_%d_%d", os.Getpid(), schemaSeq.Add(1))

	// Create an isolated schema using the shared pool.
	_, err := sharedPool.Exec(fmt.Sprintf("CREATE SCHEMA %s", schema))
	require.NoError(t, err)

	// Build a DSN that sets search_path to the isolated schema.
	schemaDSN := sharedDSN
	if strings.Contains(schemaDSN, "?") {
		schemaDSN += "&search_path=" + schema
	} else {
		schemaDSN += "?search_path=" + schema
	}

	// Use sql.Open directly so we can set pool limits BEFORE any connections are made.
	// New() sets maxOpenConns=25 which exhausts the container in CI.
	testDB, err := sql.Open("postgres", schemaDSN)
	require.NoError(t, err)
	testDB.SetMaxOpenConns(3)
	testDB.SetMaxIdleConns(2)
	require.NoError(t, testDB.Ping())
	require.NoError(t, runMigrations(testDB))
	store := &Store{db: testDB, pool: testDB}

	t.Cleanup(func() {
		store.Close()
		sharedPool.Exec(fmt.Sprintf("DROP SCHEMA %s CASCADE", schema))
	})

	return store
}

func TestNew_ConnectsAndMigrates(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)

	var version int
	err := store.db.QueryRowContext(ctx, "SELECT COALESCE(MAX(version), 0) FROM schema_version").Scan(&version)
	assert.NoError(t, err)
	assert.Greater(t, version, 0)
}

func TestApplyAppName(t *testing.T) {
	tests := []struct {
		name    string
		dsn     string
		appName string
		want    string
	}{
		{"empty app name", "postgres://host/db", "", "postgres://host/db"},
		{"already set", "postgres://host/db?application_name=foo", "bar", "postgres://host/db?application_name=foo"},
		{"uri no params", "postgres://host/db", "devpulse-site", "postgres://host/db?application_name=devpulse-site"},
		{"uri with params", "postgres://host/db?sslmode=disable", "devpulse-import", "postgres://host/db?sslmode=disable&application_name=devpulse-import"},
		{"postgresql scheme", "postgresql://host/db", "devpulse-admin", "postgresql://host/db?application_name=devpulse-admin"},
		{"keyword format", "host=localhost dbname=thingz", "devtrace-site", "host=localhost dbname=thingz application_name=devtrace-site"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, applyAppName(tc.dsn, tc.appName))
		})
	}
}

func TestApplyTimeZoneUTC(t *testing.T) {
	tests := []struct {
		name string
		dsn  string
		want string
	}{
		{"already set", "postgres://host/db?timezone=UTC", "postgres://host/db?timezone=UTC"},
		{"uri no params", "postgres://host/db", "postgres://host/db?timezone=UTC"},
		{"uri with params", "postgres://host/db?sslmode=disable", "postgres://host/db?sslmode=disable&timezone=UTC"},
		{"postgresql scheme", "postgresql://host/db", "postgresql://host/db?timezone=UTC"},
		{"keyword format", "host=localhost dbname=thingz", "host=localhost dbname=thingz timezone=UTC"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, applyTimeZoneUTC(tc.dsn))
		})
	}
}

func TestNew_EmptyDSN(t *testing.T) {
	_, err := New("")
	assert.Error(t, err)
}

func TestNew_BadDSN(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping postgres integration test in short mode")
	}
	_, err := New("postgres://invalid:invalid@localhost:1/nonexistent?sslmode=disable&connect_timeout=1")
	assert.Error(t, err)
}

// daysAgo returns a date literal N days before now, formatted for SQL DATE
// columns.
//
// Test fixtures must not hardcode absolute dates. Every query in this package
// that takes a `days` argument filters through sinceDate(), which is relative
// to time.Now(), so a fixture pinned to a literal date silently ages out of the
// window and the test starts failing on a calendar boundary rather than on a
// code change. That is exactly what happened to the metric-history and
// repo-overview tests, which began failing in September 2026 for rows written
// in March.
//
// Uses the same clock and the same UTC basis as sinceDate, so fixtures and the
// window they are selected by can never disagree.
func daysAgo(n int) string {
	return time.Now().UTC().AddDate(0, 0, -n).Format("2006-01-02")
}
