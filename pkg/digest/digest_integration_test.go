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

package digest

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	_ "github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
	"github.com/thingzio/devpulse/pkg/data/postgres"
)

var sharedDSN string
var schemaSeq atomic.Uint64
var sharedPool *sql.DB

func TestMain(m *testing.M) {
	for _, arg := range os.Args[1:] {
		if arg == "-test.short" || arg == "-test.short=true" {
			os.Exit(m.Run())
		}
	}

	var cleanup func()
	if dsn := os.Getenv("DEVPULSE_TEST_DSN"); dsn != "" {
		sharedDSN = dsn
		cleanup = func() {}
	} else {
		ctx := context.Background()
		container, err := tcpostgres.Run(ctx, "postgres:17-alpine",
			tcpostgres.WithDatabase("digest_test"),
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

func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()

	if testing.Short() {
		t.Skip("skipping postgres integration test in short mode")
	}

	schema := fmt.Sprintf("test_%d", schemaSeq.Add(1))

	_, err := sharedPool.Exec(fmt.Sprintf("CREATE SCHEMA %s", schema))
	require.NoError(t, err)

	schemaDSN := sharedDSN
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
		testDB.Close()
		sharedPool.Exec(fmt.Sprintf("DROP SCHEMA %s CASCADE", schema))
	})

	return testDB
}

const insertTestTenantSQL = `
	INSERT INTO devpulse_tenant (github_id, username, email, weekly_digest)
	VALUES ($1, $2, $3, $4)`

func TestRun_NoContent(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	// Insert a tenant with email and weekly_digest enabled.
	_, err := db.ExecContext(ctx, insertTestTenantSQL, 1001, "alice", "alice@example.com", true)
	require.NoError(t, err)

	// Insert a tenant with digest disabled.
	_, err = db.ExecContext(ctx, insertTestTenantSQL, 1002, "bob", "bob@example.com", false)
	require.NoError(t, err)

	// Insert a tenant with no email (should be skipped by listing query).
	_, err = db.ExecContext(ctx, insertTestTenantSQL, 1003, "carol", "", true)
	require.NoError(t, err)

	cfg := &Config{
		ResendAPIKey: "re_test_key",
		BaseURL:      "https://devpulse.example.com",
		HMACSecret:   "test-secret",
		TestUsername: "alice",
	}

	// No signals or portfolio data, so sendDigest skips without calling Resend.
	err = Run(ctx, db, cfg)
	assert.NoError(t, err)
}

func TestRun_TestUsername(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	_, err := db.ExecContext(ctx, insertTestTenantSQL, 2001, "target", "target@example.com", true)
	require.NoError(t, err)

	_, err = db.ExecContext(ctx, insertTestTenantSQL, 2002, "other", "other@example.com", true)
	require.NoError(t, err)

	cfg := &Config{
		ResendAPIKey: "re_test_key",
		BaseURL:      "https://devpulse.example.com",
		HMACSecret:   "test-secret",
		TestUsername: "target",
	}

	err = Run(ctx, db, cfg)
	assert.NoError(t, err)
}

func TestRun_EmptyTenants(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	cfg := &Config{
		ResendAPIKey: "re_test_key",
		BaseURL:      "https://example.com",
		HMACSecret:   "secret",
	}

	err := Run(ctx, db, cfg)
	assert.NoError(t, err)
}
