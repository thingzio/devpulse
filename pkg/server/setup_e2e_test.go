package server

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
	"github.com/thingzio/devpulse/pkg/data/postgres"
)

// Mirrors the testcontainer-based shared-pool / per-test-schema pattern
// used by pkg/tenant. TestMain spins up one Postgres container; each
// test gets its own isolated schema. Set DEVPULSE_TEST_DSN to reuse an
// already-running DB.

var (
	e2eSharedDSN  string
	e2eSchemaSeq  atomic.Uint64
	e2eSharedPool *sql.DB
)

func TestMain(m *testing.M) {
	for _, arg := range os.Args[1:] {
		if arg == "-test.short" || arg == "-test.short=true" {
			os.Exit(m.Run())
		}
	}

	var cleanup func()
	if dsn := os.Getenv("DEVPULSE_TEST_DSN"); dsn != "" {
		e2eSharedDSN = dsn
		cleanup = func() {}
	} else {
		ctx := context.Background()
		container, err := tcpostgres.Run(ctx, "postgres:17-alpine",
			tcpostgres.WithDatabase("server_e2e"),
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
			_ = container.Terminate(ctx)
			fmt.Fprintf(os.Stderr, "failed to get connection string: %v\n", err)
			os.Exit(1)
		}
		e2eSharedDSN = dsn
		cleanup = func() { _ = container.Terminate(ctx) }
	}

	var err error
	e2eSharedPool, err = sql.Open("postgres", e2eSharedDSN)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to open shared pool: %v\n", err)
		os.Exit(1)
	}
	e2eSharedPool.SetMaxOpenConns(5)
	e2eSharedPool.SetMaxIdleConns(2)

	code := m.Run()
	_ = e2eSharedPool.Close()
	cleanup()
	os.Exit(code)
}

// setupE2EDB creates an isolated schema and applies all SaaS migrations
// inside it. Returns a *sql.DB pinned to that schema. Skips in -short mode.
func setupE2EDB(t *testing.T) *sql.DB {
	t.Helper()

	if testing.Short() {
		t.Skip("skipping postgres integration test in short mode")
	}

	schema := fmt.Sprintf("test_%d", e2eSchemaSeq.Add(1))
	_, err := e2eSharedPool.Exec(fmt.Sprintf("CREATE SCHEMA %s", schema))
	require.NoError(t, err)

	schemaDSN := e2eSharedDSN
	if strings.Contains(schemaDSN, "?") {
		schemaDSN += "&search_path=" + schema
	} else {
		schemaDSN += "?search_path=" + schema
	}

	testDB, err := sql.Open("postgres", schemaDSN)
	require.NoError(t, err)
	testDB.SetMaxOpenConns(3)
	testDB.SetMaxIdleConns(2)
	require.NoError(t, testDB.Ping())

	store, err := postgres.New(schemaDSN)
	require.NoError(t, err)
	require.NoError(t, postgres.RunSaaSMigrations(store.DB()))
	store.Close()

	t.Cleanup(func() {
		_ = testDB.Close()
		_, _ = e2eSharedPool.Exec(fmt.Sprintf("DROP SCHEMA %s CASCADE", schema))
	})

	return testDB
}
