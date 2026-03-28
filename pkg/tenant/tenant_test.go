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

func TestMain(m *testing.M) {
	for _, arg := range os.Args[1:] {
		if arg == "-test.short" || arg == "-test.short=true" {
			os.Exit(m.Run())
		}
	}

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

	code := m.Run()
	container.Terminate(ctx)
	os.Exit(code)
}

func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()

	if testing.Short() {
		t.Skip("skipping postgres integration test in short mode")
	}

	schema := fmt.Sprintf("test_%d", schemaSeq.Add(1))

	db, err := sql.Open("postgres", sharedDSN)
	require.NoError(t, err)

	_, err = db.Exec(fmt.Sprintf("CREATE SCHEMA %s", schema))
	require.NoError(t, err)
	require.NoError(t, db.Close())

	schemaDSN := sharedDSN
	if strings.Contains(schemaDSN, "?") {
		schemaDSN += "&search_path=" + schema
	} else {
		schemaDSN += "?search_path=" + schema
	}

	// Run base migrations
	store, err := postgres.New(schemaDSN)
	require.NoError(t, err)

	// Run SaaS migrations
	err = postgres.RunSaaSMigrations(store.DB())
	require.NoError(t, err)

	rawDB := store.DB()

	t.Cleanup(func() {
		store.Close()
		cleanDB, cErr := sql.Open("postgres", sharedDSN)
		if cErr == nil {
			cleanDB.Exec(fmt.Sprintf("DROP SCHEMA %s CASCADE", schema))
			cleanDB.Close()
		}
	})

	return rawDB
}

func TestUpsertAndGetTenant(t *testing.T) {
	db := setupTestDB(t)

	ctx := context.Background()

	tn, err := UpsertTenant(ctx, db, 12345, "testuser", "test@example.com", "https://avatar.url")
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
	tn2, err := UpsertTenant(ctx, db, 12345, "newname", "new@example.com", "https://new.url")
	require.NoError(t, err)
	assert.Equal(t, tn.ID, tn2.ID)
	assert.Equal(t, "newname", tn2.Username)
}

func TestGetTenantByID(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	tn, err := UpsertTenant(ctx, db, 33333, "iduser", "", "")
	require.NoError(t, err)

	got, err := GetTenantByID(ctx, db, tn.ID)
	require.NoError(t, err)
	assert.Equal(t, tn.GitHubID, got.GitHubID)
}

func TestAcceptToS(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	tn, err := UpsertTenant(ctx, db, 99999, "tosuser", "", "")
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

	tn, err := UpsertTenant(ctx, db, 44444, "planuser", "", "")
	require.NoError(t, err)
	assert.Equal(t, "free", tn.Plan)
	assert.Equal(t, 3, tn.MaxRepos)

	err = UpdatePlan(ctx, db, tn.ID, "pro", 20)
	require.NoError(t, err)

	got, err := GetTenantByID(ctx, db, tn.ID)
	require.NoError(t, err)
	assert.Equal(t, "pro", got.Plan)
	assert.Equal(t, 20, got.MaxRepos)
}

func TestSessionLifecycle(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	tn, err := UpsertTenant(ctx, db, 55555, "sessuser", "", "")
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

	tn, err := UpsertTenant(ctx, db, 55556, "expuser", "", "")
	require.NoError(t, err)

	rawToken, err := CreateSession(ctx, db, tn.ID, -1*time.Hour)
	require.NoError(t, err)

	_, err = ValidateSession(ctx, db, rawToken)
	require.Error(t, err)
}

func TestCleanExpiredSessions(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	tn, err := UpsertTenant(ctx, db, 55557, "cleanuser", "", "")
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
