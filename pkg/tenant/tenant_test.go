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

package tenant

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
			tcpostgres.WithDatabase("tenant_test"),
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

	// PID prefix prevents schema-name collisions when CI runs pkg/data/postgres,
	// pkg/server, and pkg/tenant in parallel against the shared DEVPULSE_TEST_DSN.
	schema := fmt.Sprintf("test_%d_%d", os.Getpid(), schemaSeq.Add(1))

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

func TestUpsertAndGetTenant(t *testing.T) {
	db := setupTestDB(t)

	ctx := context.Background()

	// Create tenant without profile fields (simulates pre-existing user).
	tn, err := UpsertTenant(ctx, db, 12345, "testuser", "test@example.com", "https://avatar.url", "", "", "", "")
	require.NoError(t, err)
	require.NotEmpty(t, tn.ID)
	assert.Equal(t, int64(12345), tn.GitHubID)
	assert.Equal(t, "testuser", tn.Username)
	assert.Equal(t, "test@example.com", tn.Email)
	assert.Equal(t, 25, tn.MaxRepos)
	assert.Equal(t, "pro", tn.Plan)
	// Profile fields empty when not provided.
	assert.Empty(t, tn.Name)
	assert.Empty(t, tn.Company)
	assert.Empty(t, tn.Location)
	assert.Empty(t, tn.Bio)

	got, err := GetTenantByGitHubID(ctx, db, 12345)
	require.NoError(t, err)
	assert.Equal(t, tn.ID, got.ID)
	assert.Empty(t, got.Name)

	// Upsert with profile fields (simulates re-authentication).
	tn2, err := UpsertTenant(ctx, db, 12345, "newname", "new@example.com", "https://new.url",
		"New User", "NVIDIA", "Portland, OR", "Building things")
	require.NoError(t, err)
	assert.Equal(t, tn.ID, tn2.ID)
	assert.Equal(t, "newname", tn2.Username)
	assert.Equal(t, "New User", tn2.Name)
	assert.Equal(t, "NVIDIA", tn2.Company)
	assert.Equal(t, "Portland, OR", tn2.Location)
	assert.Equal(t, "Building things", tn2.Bio)

	// Verify profile fields persist through GetTenantByID.
	got2, err := GetTenantByID(ctx, db, tn.ID)
	require.NoError(t, err)
	assert.Equal(t, "New User", got2.Name)
	assert.Equal(t, "NVIDIA", got2.Company)
	assert.Equal(t, "Portland, OR", got2.Location)
	assert.Equal(t, "Building things", got2.Bio)
}

func TestGetTenantByID(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	tn, err := UpsertTenant(ctx, db, 33333, "iduser", "", "", "", "", "", "")
	require.NoError(t, err)

	got, err := GetTenantByID(ctx, db, tn.ID)
	require.NoError(t, err)
	assert.Equal(t, tn.GitHubID, got.GitHubID)
}

func TestAcceptToS(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	tn, err := UpsertTenant(ctx, db, 99999, "tosuser", "", "", "", "", "", "")
	require.NoError(t, err)
	require.Nil(t, tn.ToSAcceptedAt)

	err = AcceptToS(ctx, db, tn.ID)
	require.NoError(t, err)

	got, err := GetTenantByGitHubID(ctx, db, 99999)
	require.NoError(t, err)
	require.NotNil(t, got.ToSAcceptedAt)
}

func TestUpdatePlan(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	tn, err := UpsertTenant(ctx, db, 44444, "planuser", "", "", "", "", "", "")
	require.NoError(t, err)
	assert.Equal(t, "pro", tn.Plan)
	assert.Equal(t, 25, tn.MaxRepos)
	assert.Equal(t, 15000, tn.MaxEventsPerWeek)

	err = UpdatePlan(ctx, db, tn.ID, "pro", 25, 20000)
	require.NoError(t, err)

	got, err := GetTenantByID(ctx, db, tn.ID)
	require.NoError(t, err)
	assert.Equal(t, "pro", got.Plan)
	assert.Equal(t, 25, got.MaxRepos)
	assert.Equal(t, 20000, got.MaxEventsPerWeek)
}

func TestSessionLifecycle(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	tn, err := UpsertTenant(ctx, db, 55555, "sessuser", "", "", "", "", "", "")
	require.NoError(t, err)

	rawToken, err := CreateSession(ctx, db, tn.ID, 7*24*time.Hour)
	require.NoError(t, err)
	require.NotEmpty(t, rawToken)

	got, err := ValidateSession(ctx, db, rawToken)
	require.NoError(t, err)
	assert.Equal(t, tn.ID, got.ID)

	err = DestroySession(ctx, db, rawToken)
	require.NoError(t, err)

	_, err = ValidateSession(ctx, db, rawToken)
	require.Error(t, err)
}

func TestExpiredSession(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	tn, err := UpsertTenant(ctx, db, 55556, "expuser", "", "", "", "", "", "")
	require.NoError(t, err)

	rawToken, err := CreateSession(ctx, db, tn.ID, -1*time.Hour)
	require.NoError(t, err)

	_, err = ValidateSession(ctx, db, rawToken)
	require.Error(t, err)
}

func TestCleanExpiredSessions(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	tn, err := UpsertTenant(ctx, db, 55557, "cleanuser", "", "", "", "", "", "")
	require.NoError(t, err)

	// Create expired session
	_, err = CreateSession(ctx, db, tn.ID, -1*time.Hour)
	require.NoError(t, err)

	// Create valid session
	_, err = CreateSession(ctx, db, tn.ID, 7*24*time.Hour)
	require.NoError(t, err)

	cleaned, err := CleanExpiredSessions(ctx, db)
	require.NoError(t, err)
	assert.Equal(t, int64(1), cleaned)
}

func TestGetLastSignIn(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	t.Run("nil when no sessions", func(t *testing.T) {
		tn, err := UpsertTenant(ctx, db, 55558, "nosessuser", "", "", "", "", "", "")
		require.NoError(t, err)
		assert.Nil(t, GetLastSignIn(ctx, db, tn.ID))
	})

	t.Run("returns most recent session time", func(t *testing.T) {
		tn, err := UpsertTenant(ctx, db, 55559, "signinuser", "", "", "", "", "", "")
		require.NoError(t, err)

		_, err = CreateSession(ctx, db, tn.ID, 7*24*time.Hour)
		require.NoError(t, err)

		got := GetLastSignIn(ctx, db, tn.ID)
		require.NotNil(t, got)
		assert.WithinDuration(t, time.Now(), *got, 5*time.Second)
	})
}
