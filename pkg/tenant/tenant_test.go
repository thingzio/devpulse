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

func TestUpsertAndGetTenant(t *testing.T) {
	db := setupTestDB(t)

	ctx := context.Background()

	tn, err := UpsertTenant(ctx, db, 12345, "testuser", "test@example.com", "https://avatar.url", "", "", "", "")
	require.NoError(t, err)
	require.NotEmpty(t, tn.ID)
	assert.Equal(t, int64(12345), tn.GitHubID)
	assert.Equal(t, "testuser", tn.Username)
	assert.Equal(t, 3, tn.MaxRepos)
	assert.Equal(t, "free", tn.Plan)

	got, err := GetTenantByGitHubID(ctx, db, 12345)
	require.NoError(t, err)
	assert.Equal(t, tn.ID, got.ID)

	// Upsert updates profile fields
	tn2, err := UpsertTenant(ctx, db, 12345, "newname", "new@example.com", "https://new.url", "", "", "", "")
	require.NoError(t, err)
	assert.Equal(t, tn.ID, tn2.ID)
	assert.Equal(t, "newname", tn2.Username)
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
	assert.Equal(t, "free", tn.Plan)
	assert.Equal(t, 3, tn.MaxRepos)
	assert.Equal(t, 1000, tn.MaxEventsPerWeek)

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
