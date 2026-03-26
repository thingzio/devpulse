# Multi-Tenant SaaS Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Transform DevPulse into a multi-tenant SaaS with GitHub OAuth identity, GitHub App data access, PostgreSQL RLS isolation, and Cloud Run scale-to-zero deployment.

**Architecture:** Monorepo with two binaries (`cmd/devpulse/` unchanged, new `cmd/devpulse-cloud/`). Shared `pkg/data/` query layer. Tenant isolation via PostgreSQL RLS policies that filter by `app.tenant_id` session variable. GitHub App for scoped repo access. Server-side sessions with hashed tokens.

**Tech Stack:** Go 1.26, PostgreSQL (Cloud SQL), Cloud Run, `github.com/google/go-github/v83`, `github.com/golang-jwt/jwt/v5` (new dep for GitHub App JWT), `github.com/urfave/cli/v3`, `github.com/stretchr/testify`.

**Design Doc:** `docs/plans/2026-03-26-multi-tenant-saas-design.md`

---

## Phase 1: Foundation (Tenant Data Model + SaaS Entrypoint)

### Task 1: PostgreSQL SaaS Migration — Tenant Tables

Create the tenant, session, GitHub App installation, tenant_repo, and tenant_member tables in a new SaaS-specific migration directory.

**Files:**
- Create: `pkg/data/postgres/sql/migrations_saas/001_tenant_tables.sql`

**Step 1: Write the migration SQL**

```sql
-- Tenant tables for multi-tenant SaaS

CREATE TABLE IF NOT EXISTS tenant (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    github_id       BIGINT UNIQUE NOT NULL,
    username        TEXT NOT NULL,
    email           TEXT,
    avatar_url      TEXT,
    max_repos       INT NOT NULL DEFAULT 3,
    plan            TEXT NOT NULL DEFAULT 'free',
    tos_accepted_at TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS tenant_member (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   UUID NOT NULL REFERENCES tenant(id) ON DELETE CASCADE,
    github_id   BIGINT NOT NULL,
    username    TEXT NOT NULL,
    role        TEXT NOT NULL DEFAULT 'viewer',
    invited_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    accepted_at TIMESTAMPTZ,
    UNIQUE(tenant_id, github_id)
);

CREATE TABLE IF NOT EXISTS github_app_installation (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id           UUID NOT NULL REFERENCES tenant(id) ON DELETE CASCADE,
    installation_id     BIGINT UNIQUE NOT NULL,
    target_type         TEXT NOT NULL,
    target_login        TEXT NOT NULL,
    permissions         JSONB,
    suspended_at        TIMESTAMPTZ,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS tenant_repo (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   UUID NOT NULL REFERENCES tenant(id) ON DELETE CASCADE,
    org         TEXT NOT NULL,
    repo        TEXT NOT NULL,
    reputation  JSONB,
    insight     JSONB,
    active      BOOLEAN NOT NULL DEFAULT TRUE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(tenant_id, org, repo)
);

CREATE TABLE IF NOT EXISTS session (
    id          TEXT PRIMARY KEY,
    tenant_id   UUID NOT NULL REFERENCES tenant(id) ON DELETE CASCADE,
    expires_at  TIMESTAMPTZ NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_session_tenant ON session(tenant_id);
CREATE INDEX idx_session_expires ON session(expires_at);
CREATE INDEX idx_tenant_repo_tenant ON tenant_repo(tenant_id, active);
CREATE INDEX idx_tenant_member_github ON tenant_member(github_id);
CREATE INDEX idx_github_app_installation_tenant ON github_app_installation(tenant_id);
```

**Step 2: Commit**

```bash
git add pkg/data/postgres/sql/migrations_saas/
git commit -S -m "feat: add SaaS tenant tables migration"
```

---

### Task 2: SaaS Migration Runner

Add a function to run SaaS-specific migrations from the `migrations_saas/` directory, callable by `devpulse-cloud` but not by the CLI binary.

**Files:**
- Create: `pkg/data/postgres/saas.go`
- Create: `pkg/data/postgres/saas_test.go`

**Step 1: Write the failing test**

```go
package postgres

import (
    "testing"

    "github.com/stretchr/testify/require"
)

func TestRunSaaSMigrations(t *testing.T) {
    if testing.Short() {
        t.Skip("skipping integration test")
    }

    store := setupTestDB(t)
    db := store.DB()

    err := RunSaaSMigrations(db)
    require.NoError(t, err)

    // Verify tenant table exists
    var exists bool
    err = db.QueryRow(`SELECT EXISTS (
        SELECT 1 FROM information_schema.tables
        WHERE table_name = 'tenant'
    )`).Scan(&exists)
    require.NoError(t, err)
    require.True(t, exists)

    // Verify idempotency
    err = RunSaaSMigrations(db)
    require.NoError(t, err)
}
```

**Step 2: Run test to verify it fails**

Run: `make test`
Expected: FAIL — `RunSaaSMigrations` not defined

**Step 3: Write the implementation**

```go
package postgres

import (
    "database/sql"
    "embed"
    "fmt"
    "log/slog"
    "sort"
    "strings"
)

//go:embed sql/migrations_saas/*.sql
var saasMigrationsFS embed.FS

// RunSaaSMigrations runs SaaS-specific migrations (tenant tables, RLS policies).
// Called by devpulse-cloud only, not the self-hosted CLI.
func RunSaaSMigrations(db *sql.DB) error {
    if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS saas_schema_version (
        version INTEGER PRIMARY KEY,
        applied_at TIMESTAMP NOT NULL DEFAULT NOW()
    )`); err != nil {
        return fmt.Errorf("creating saas_schema_version table: %w", err)
    }

    if _, err := db.Exec("SELECT pg_advisory_lock(2)"); err != nil {
        return fmt.Errorf("acquiring saas migration lock: %w", err)
    }
    defer func() { _, _ = db.Exec("SELECT pg_advisory_unlock(2)") }()

    var currentVersion int
    if err := db.QueryRow("SELECT COALESCE(MAX(version), 0) FROM saas_schema_version").Scan(&currentVersion); err != nil {
        return fmt.Errorf("reading saas schema version: %w", err)
    }

    entries, err := saasMigrationsFS.ReadDir("sql/migrations_saas")
    if err != nil {
        return fmt.Errorf("reading saas migrations dir: %w", err)
    }

    sort.Slice(entries, func(i, j int) bool {
        return entries[i].Name() < entries[j].Name()
    })

    for _, entry := range entries {
        if entry.IsDir() {
            continue
        }

        name := entry.Name()
        parts := strings.SplitN(name, "_", 2)
        if len(parts) < 2 {
            continue
        }

        var ver int
        if _, err := fmt.Sscanf(parts[0], "%d", &ver); err != nil {
            continue
        }

        if ver <= currentVersion {
            continue
        }

        content, err := saasMigrationsFS.ReadFile("sql/migrations_saas/" + name)
        if err != nil {
            return fmt.Errorf("reading saas migration %s: %w", name, err)
        }

        slog.Debug("applying saas migration", "version", ver, "file", name)

        tx, err := db.Begin()
        if err != nil {
            return fmt.Errorf("beginning saas migration tx %d: %w", ver, err)
        }

        if _, err := tx.Exec(string(content)); err != nil {
            _ = tx.Rollback()
            return fmt.Errorf("executing saas migration %s: %w", name, err)
        }

        if _, err := tx.Exec("INSERT INTO saas_schema_version (version) VALUES ($1)", ver); err != nil {
            _ = tx.Rollback()
            return fmt.Errorf("recording saas migration %d: %w", ver, err)
        }

        if err := tx.Commit(); err != nil {
            return fmt.Errorf("committing saas migration %d: %w", ver, err)
        }

        slog.Info("applied saas migration", "version", ver, "file", name)
    }

    return nil
}
```

**Step 4: Run tests**

Run: `make test`
Expected: PASS

**Step 5: Commit**

```bash
git add pkg/data/postgres/saas.go pkg/data/postgres/saas_test.go
git commit -S -m "feat: add SaaS migration runner"
```

---

### Task 3: Tenant CRUD Package

Core tenant management: create, get by GitHub ID, list, update plan.

**Files:**
- Create: `pkg/tenant/tenant.go`
- Create: `pkg/tenant/tenant_test.go`

**Step 1: Write the failing test**

```go
package tenant

import (
    "database/sql"
    "testing"

    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
)

func TestUpsertAndGetTenant(t *testing.T) {
    db := setupTestDB(t) // helper that creates PG + runs base + saas migrations

    // Create tenant
    tn, err := UpsertTenant(db, 12345, "testuser", "test@example.com", "https://avatar.url")
    require.NoError(t, err)
    require.NotEmpty(t, tn.ID)
    assert.Equal(t, int64(12345), tn.GitHubID)
    assert.Equal(t, "testuser", tn.Username)
    assert.Equal(t, 3, tn.MaxRepos) // default
    assert.Equal(t, "free", tn.Plan)

    // Get by GitHub ID
    got, err := GetTenantByGitHubID(db, 12345)
    require.NoError(t, err)
    assert.Equal(t, tn.ID, got.ID)

    // Upsert updates profile fields
    tn2, err := UpsertTenant(db, 12345, "newname", "new@example.com", "https://new.url")
    require.NoError(t, err)
    assert.Equal(t, tn.ID, tn2.ID)       // same tenant
    assert.Equal(t, "newname", tn2.Username) // updated
}

func TestAcceptToS(t *testing.T) {
    db := setupTestDB(t)

    tn, err := UpsertTenant(db, 99999, "tosuser", "", "")
    require.NoError(t, err)
    require.Nil(t, tn.ToSAcceptedAt)

    err = AcceptToS(db, tn.ID)
    require.NoError(t, err)

    got, err := GetTenantByGitHubID(db, 99999)
    require.NoError(t, err)
    require.NotNil(t, got.ToSAcceptedAt)
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./pkg/tenant/ -v -short`
Expected: FAIL — package doesn't exist

**Step 3: Write the implementation**

```go
package tenant

import (
    "database/sql"
    "fmt"
    "time"
)

// Tenant represents a registered SaaS tenant.
type Tenant struct {
    ID            string
    GitHubID      int64
    Username      string
    Email         string
    AvatarURL     string
    MaxRepos      int
    Plan          string
    ToSAcceptedAt *time.Time
    CreatedAt     time.Time
    UpdatedAt     time.Time
}

const upsertTenantSQL = `
    INSERT INTO tenant (github_id, username, email, avatar_url)
    VALUES ($1, $2, $3, $4)
    ON CONFLICT (github_id) DO UPDATE SET
        username = EXCLUDED.username,
        email = EXCLUDED.email,
        avatar_url = EXCLUDED.avatar_url,
        updated_at = NOW()
    RETURNING id, github_id, username, email, avatar_url, max_repos, plan,
              tos_accepted_at, created_at, updated_at`

const getTenantByGitHubIDSQL = `
    SELECT id, github_id, username, email, avatar_url, max_repos, plan,
           tos_accepted_at, created_at, updated_at
    FROM tenant WHERE github_id = $1`

const acceptToSSQL = `UPDATE tenant SET tos_accepted_at = NOW(), updated_at = NOW() WHERE id = $1`

func UpsertTenant(db *sql.DB, githubID int64, username, email, avatarURL string) (*Tenant, error) {
    var t Tenant
    err := db.QueryRow(upsertTenantSQL, githubID, username, email, avatarURL).Scan(
        &t.ID, &t.GitHubID, &t.Username, &t.Email, &t.AvatarURL,
        &t.MaxRepos, &t.Plan, &t.ToSAcceptedAt, &t.CreatedAt, &t.UpdatedAt,
    )
    if err != nil {
        return nil, fmt.Errorf("upserting tenant: %w", err)
    }
    return &t, nil
}

func GetTenantByGitHubID(db *sql.DB, githubID int64) (*Tenant, error) {
    var t Tenant
    err := db.QueryRow(getTenantByGitHubIDSQL, githubID).Scan(
        &t.ID, &t.GitHubID, &t.Username, &t.Email, &t.AvatarURL,
        &t.MaxRepos, &t.Plan, &t.ToSAcceptedAt, &t.CreatedAt, &t.UpdatedAt,
    )
    if err != nil {
        return nil, fmt.Errorf("getting tenant by github_id: %w", err)
    }
    return &t, nil
}

func AcceptToS(db *sql.DB, tenantID string) error {
    _, err := db.Exec(acceptToSSQL, tenantID)
    if err != nil {
        return fmt.Errorf("accepting ToS: %w", err)
    }
    return nil
}
```

**Step 4: Write the test helper**

```go
package tenant

import (
    "database/sql"
    "testing"

    // Import postgres package for migrations
    pgstore "github.com/mchmarny/devpulse/pkg/data/postgres"
)

func setupTestDB(t *testing.T) *sql.DB {
    t.Helper()
    // Use testcontainers-go to spin up a Postgres container
    // Run base migrations via pgstore.New(), then SaaS migrations
    store, err := pgstore.NewTestStore(t)
    if err != nil {
        t.Fatal(err)
    }
    db := store.DB()
    if err := pgstore.RunSaaSMigrations(db); err != nil {
        t.Fatal(err)
    }
    return db
}
```

Note: `NewTestStore` should be extracted from the existing `setupTestDB` in `pkg/data/postgres/postgres_test.go`. This is a refactoring task — see Task 4.

**Step 5: Run tests**

Run: `make test`
Expected: PASS

**Step 6: Commit**

```bash
git add pkg/tenant/
git commit -S -m "feat: add tenant CRUD package"
```

---

### Task 4: Extract Shared Test Helper for PostgreSQL

The existing `setupTestDB(t)` in `pkg/data/postgres/postgres_test.go` uses testcontainers. Extract a reusable `NewTestStore(t)` function so `pkg/tenant/` tests can also create a test Postgres instance.

**Files:**
- Modify: `pkg/data/postgres/postgres_test.go` — extract `NewTestStore`
- Create: `pkg/data/postgres/testhelper_test.go` (or export from a `testing.go` build-tagged file)

**Step 1: Read existing test setup**

Read `pkg/data/postgres/postgres_test.go` to understand the current `setupTestDB` implementation.

**Step 2: Extract `NewTestStore` as an exported function**

Create `pkg/data/postgres/testhelper.go` with build tag `//go:build testing` or use an exported function gated by the `testing` package:

```go
package postgres

import (
    "context"
    "testing"
    "time"

    "github.com/testcontainers/testcontainers-go"
    pgmodule "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// NewTestDB creates a temporary PostgreSQL container and returns a Store
// with all base migrations applied. Caller must call store.Close() or
// use t.Cleanup.
func NewTestDB(t *testing.T) *Store {
    t.Helper()
    // Move existing setupTestDB logic here, exported
    // ...
}
```

**Step 3: Update existing tests to use `NewTestDB`**

Replace `setupTestDB(t)` calls with `NewTestDB(t)` in all `postgres/*_test.go` files.

**Step 4: Run tests**

Run: `make test`
Expected: PASS — all existing tests still work

**Step 5: Commit**

```bash
git add pkg/data/postgres/
git commit -S -m "refactor: extract shared PostgreSQL test helper"
```

---

### Task 5: Session Management

Create, validate, and destroy server-side sessions.

**Files:**
- Create: `pkg/tenant/session.go`
- Create: `pkg/tenant/session_test.go`

**Step 1: Write the failing test**

```go
package tenant

import (
    "testing"
    "time"

    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
)

func TestSessionLifecycle(t *testing.T) {
    db := setupTestDB(t)

    tn, err := UpsertTenant(db, 55555, "sessuser", "", "")
    require.NoError(t, err)

    // Create session
    rawToken, err := CreateSession(db, tn.ID, 7*24*time.Hour)
    require.NoError(t, err)
    require.NotEmpty(t, rawToken)

    // Validate session
    got, err := ValidateSession(db, rawToken)
    require.NoError(t, err)
    assert.Equal(t, tn.ID, got.ID)

    // Destroy session
    err = DestroySession(db, rawToken)
    require.NoError(t, err)

    // Validate should fail
    _, err = ValidateSession(db, rawToken)
    require.Error(t, err)
}

func TestExpiredSession(t *testing.T) {
    db := setupTestDB(t)

    tn, err := UpsertTenant(db, 55556, "expuser", "", "")
    require.NoError(t, err)

    rawToken, err := CreateSession(db, tn.ID, -1*time.Hour) // already expired
    require.NoError(t, err)

    _, err = ValidateSession(db, rawToken)
    require.Error(t, err)
}
```

**Step 2: Run test to verify it fails**

Expected: FAIL — functions not defined

**Step 3: Write the implementation**

```go
package tenant

import (
    "crypto/rand"
    "crypto/sha256"
    "database/sql"
    "encoding/hex"
    "errors"
    "fmt"
    "time"
)

var errSessionExpired = errors.New("session expired or not found")

const createSessionSQL = `INSERT INTO session (id, tenant_id, expires_at) VALUES ($1, $2, $3)`

const validateSessionSQL = `
    SELECT t.id, t.github_id, t.username, t.email, t.avatar_url,
           t.max_repos, t.plan, t.tos_accepted_at, t.created_at, t.updated_at
    FROM session s
    JOIN tenant t ON t.id = s.tenant_id
    WHERE s.id = $1 AND s.expires_at > NOW()`

const destroySessionSQL = `DELETE FROM session WHERE id = $1`

func CreateSession(db *sql.DB, tenantID string, ttl time.Duration) (string, error) {
    raw := make([]byte, 32)
    if _, err := rand.Read(raw); err != nil {
        return "", fmt.Errorf("generating session token: %w", err)
    }
    rawToken := hex.EncodeToString(raw)
    hashed := hashToken(rawToken)

    _, err := db.Exec(createSessionSQL, hashed, tenantID, time.Now().Add(ttl))
    if err != nil {
        return "", fmt.Errorf("creating session: %w", err)
    }
    return rawToken, nil
}

func ValidateSession(db *sql.DB, rawToken string) (*Tenant, error) {
    hashed := hashToken(rawToken)
    var t Tenant
    err := db.QueryRow(validateSessionSQL, hashed).Scan(
        &t.ID, &t.GitHubID, &t.Username, &t.Email, &t.AvatarURL,
        &t.MaxRepos, &t.Plan, &t.ToSAcceptedAt, &t.CreatedAt, &t.UpdatedAt,
    )
    if errors.Is(err, sql.ErrNoRows) {
        return nil, errSessionExpired
    }
    if err != nil {
        return nil, fmt.Errorf("validating session: %w", err)
    }
    return &t, nil
}

func DestroySession(db *sql.DB, rawToken string) error {
    _, err := db.Exec(destroySessionSQL, hashToken(rawToken))
    if err != nil {
        return fmt.Errorf("destroying session: %w", err)
    }
    return nil
}

func hashToken(raw string) string {
    h := sha256.Sum256([]byte(raw))
    return hex.EncodeToString(h[:])
}
```

**Step 4: Run tests**

Run: `make test`
Expected: PASS

**Step 5: Commit**

```bash
git add pkg/tenant/session.go pkg/tenant/session_test.go
git commit -S -m "feat: add session management"
```

---

### Task 6: SaaS Binary Entrypoint

Create `cmd/devpulse-cloud/main.go` with `serve` and `import` subcommands. Initially wires up DB connection and runs both base + SaaS migrations.

**Files:**
- Create: `cmd/devpulse-cloud/main.go`

**Step 1: Write the entrypoint**

```go
package main

import (
    "context"
    "fmt"
    "log/slog"
    "os"
    "os/signal"
    "syscall"

    "github.com/mchmarny/devpulse/pkg/data/postgres"
    "github.com/mchmarny/devpulse/pkg/logging"
    urfave "github.com/urfave/cli/v3"
)

var (
    version = "v0.0.1-default"
    commit  = ""
    date    = ""
)

func main() {
    level := "info"
    if os.Getenv("DEVPULSE_DEBUG") == "true" || os.Getenv("DEVPULSE_DEBUG") == "1" {
        level = "debug"
    }
    logging.SetDefaultJSONLogger(level)

    ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)

    app := &urfave.Command{
        Name:            "devpulse-cloud",
        Version:         fmt.Sprintf("%s (%s - %s)", version, commit, date),
        Usage:           "DevPulse multi-tenant SaaS service",
        HideHelpCommand: true,
        Flags: []urfave.Flag{
            &urfave.StringFlag{
                Name:     "db",
                Usage:    "PostgreSQL connection URI (postgres://...)",
                Sources:  urfave.EnvVars("DATABASE_URL"),
                Required: true,
            },
        },
        Commands: []*urfave.Command{
            serveCmd,
            importWorkerCmd,
        },
        Metadata: map[string]any{},
    }

    if err := app.Run(ctx, os.Args); err != nil {
        stop()
        slog.Error("fatal error", "error", err)
        os.Exit(1)
    }
    stop()
}

func openSaaSStore(dsn string) (*postgres.Store, error) {
    store, err := postgres.New(dsn)
    if err != nil {
        return nil, fmt.Errorf("opening database: %w", err)
    }
    if err := postgres.RunSaaSMigrations(store.DB()); err != nil {
        store.Close()
        return nil, fmt.Errorf("running saas migrations: %w", err)
    }
    return store, nil
}
```

**Step 2: Add placeholder serve and import commands**

Create `cmd/devpulse-cloud/serve.go`:

```go
package main

import (
    "context"
    "log/slog"

    urfave "github.com/urfave/cli/v3"
)

var serveCmd = &urfave.Command{
    Name:  "serve",
    Usage: "Start the SaaS HTTP server",
    Flags: []urfave.Flag{
        &urfave.IntFlag{
            Name:    "port",
            Value:   8080,
            Sources: urfave.EnvVars("PORT"),
        },
    },
    Action: func(ctx context.Context, cmd *urfave.Command) error {
        dsn := cmd.Root().String("db")
        store, err := openSaaSStore(dsn)
        if err != nil {
            return err
        }
        defer store.Close()

        slog.Info("server starting", "port", cmd.Int("port"))
        // TODO: wire up HTTP server in Task 10
        <-ctx.Done()
        return nil
    },
}
```

Create `cmd/devpulse-cloud/import_worker.go`:

```go
package main

import (
    "context"
    "log/slog"

    urfave "github.com/urfave/cli/v3"
)

var importWorkerCmd = &urfave.Command{
    Name:  "import",
    Usage: "Run tenant data import worker",
    Action: func(ctx context.Context, cmd *urfave.Command) error {
        dsn := cmd.Root().String("db")
        store, err := openSaaSStore(dsn)
        if err != nil {
            return err
        }
        defer store.Close()

        slog.Info("import worker starting")
        // TODO: wire up import loop in Phase 3
        return nil
    },
}
```

**Step 3: Verify it compiles**

Run: `go build ./cmd/devpulse-cloud/`
Expected: Compiles without error

**Step 4: Commit**

```bash
git add cmd/devpulse-cloud/
git commit -S -m "feat: add devpulse-cloud SaaS entrypoint with serve and import subcommands"
```

---

### Task 7: Update goreleaser for Two Binaries

Add the `devpulse-cloud` build target and container image to `.goreleaser.yml`.

**Files:**
- Modify: `.goreleaser.yml`

**Step 1: Read the current config**

Already read — see `.goreleaser.yml` content above.

**Step 2: Add second build and ko target**

Add to `builds:` list:

```yaml
  - id: devpulse-cloud
    binary: devpulse-cloud
    dir: ./cmd/devpulse-cloud
    env:
      - CGO_ENABLED=0
    flags:
      - -trimpath
    ldflags:
      - -s -w
        -X main.version={{.Version}}
        -X main.commit={{.ShortCommit}}
        -X main.date={{.CommitDate}}
    goos:
      - linux
    goarch:
      - amd64
      - arm64
```

Add to `kos:` list:

```yaml
  - id: devpulse-cloud
    build: devpulse-cloud
    repositories:
      - ghcr.io/mchmarny/devpulse-cloud
    platforms:
      - linux/amd64
      - linux/arm64
    tags:
      - latest
      - "{{.Tag}}"
    preserve_import_paths: false
    bare: true
```

Update existing build to have an `id`:

```yaml
  - id: devpulse
    binary: "{{.ProjectName}}"
```

**Step 3: Verify goreleaser config**

Run: `goreleaser check`
Expected: valid configuration

**Step 4: Commit**

```bash
git add .goreleaser.yml
git commit -S -m "build: add devpulse-cloud to goreleaser config"
```

---

## Phase 2: Authentication (OAuth + GitHub App + Middleware)

### Task 8: GitHub OAuth Web Flow

Implement the OAuth web flow for "Sign in with GitHub". Separate from the existing Device Code flow in `pkg/auth/`.

**Files:**
- Create: `pkg/oauth/github.go`
- Create: `pkg/oauth/github_test.go`

**Step 1: Write the failing test**

```go
package oauth

import (
    "net/http"
    "net/http/httptest"
    "testing"

    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
)

func TestBuildAuthURL(t *testing.T) {
    cfg := &GitHubOAuthConfig{
        ClientID:    "test-client-id",
        RedirectURL: "https://example.com/auth/github/callback",
    }

    url, state := BuildAuthURL(cfg)
    require.NotEmpty(t, state)
    assert.Contains(t, url, "github.com/login/oauth/authorize")
    assert.Contains(t, url, "client_id=test-client-id")
    assert.Contains(t, url, "state="+state)
    assert.Contains(t, url, "scope=read%3Auser")
}

func TestExchangeCode_InvalidCode(t *testing.T) {
    // Mock GitHub token endpoint
    server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("Content-Type", "application/json")
        w.Write([]byte(`{"error":"bad_verification_code"}`))
    }))
    defer server.Close()

    cfg := &GitHubOAuthConfig{
        ClientID:     "test",
        ClientSecret: "secret",
        TokenURL:     server.URL,
    }

    _, err := ExchangeCode(r.Context(), cfg, "invalid-code")
    require.Error(t, err)
}
```

**Step 2: Write the implementation**

```go
package oauth

import (
    "context"
    "crypto/rand"
    "encoding/hex"
    "encoding/json"
    "errors"
    "fmt"
    "net/http"
    "net/url"
    "strings"
)

const (
    defaultAuthURL  = "https://github.com/login/oauth/authorize"
    defaultTokenURL = "https://github.com/login/oauth/access_token"
    defaultUserURL  = "https://api.github.com/user"
    oauthScope      = "read:user user:email"
)

type GitHubOAuthConfig struct {
    ClientID     string
    ClientSecret string
    RedirectURL  string
    AuthURL      string // override for testing
    TokenURL     string // override for testing
    UserURL      string // override for testing
}

type GitHubUser struct {
    ID        int64  `json:"id"`
    Login     string `json:"login"`
    Email     string `json:"email"`
    AvatarURL string `json:"avatar_url"`
}

func BuildAuthURL(cfg *GitHubOAuthConfig) (string, string) {
    state := randomState()
    authURL := cfg.AuthURL
    if authURL == "" {
        authURL = defaultAuthURL
    }
    v := url.Values{
        "client_id":    {cfg.ClientID},
        "redirect_uri": {cfg.RedirectURL},
        "scope":        {oauthScope},
        "state":        {state},
    }
    return authURL + "?" + v.Encode(), state
}

func ExchangeCode(ctx context.Context, cfg *GitHubOAuthConfig, code string) (string, error) {
    tokenURL := cfg.TokenURL
    if tokenURL == "" {
        tokenURL = defaultTokenURL
    }

    v := url.Values{
        "client_id":     {cfg.ClientID},
        "client_secret": {cfg.ClientSecret},
        "code":          {code},
    }

    req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(v.Encode()))
    if err != nil {
        return "", fmt.Errorf("creating token request: %w", err)
    }
    req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
    req.Header.Set("Accept", "application/json")

    resp, err := http.DefaultClient.Do(req)
    if err != nil {
        return "", fmt.Errorf("exchanging code: %w", err)
    }
    defer resp.Body.Close()

    var result struct {
        AccessToken string `json:"access_token"`
        Error       string `json:"error"`
    }
    if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
        return "", fmt.Errorf("decoding token response: %w", err)
    }
    if result.Error != "" {
        return "", fmt.Errorf("oauth error: %s", result.Error)
    }
    if result.AccessToken == "" {
        return "", errors.New("empty access token")
    }
    return result.AccessToken, nil
}

func FetchUser(ctx context.Context, cfg *GitHubOAuthConfig, token string) (*GitHubUser, error) {
    userURL := cfg.UserURL
    if userURL == "" {
        userURL = defaultUserURL
    }

    req, err := http.NewRequestWithContext(ctx, http.MethodGet, userURL, nil)
    if err != nil {
        return nil, fmt.Errorf("creating user request: %w", err)
    }
    req.Header.Set("Authorization", "Bearer "+token)
    req.Header.Set("Accept", "application/json")

    resp, err := http.DefaultClient.Do(req)
    if err != nil {
        return nil, fmt.Errorf("fetching user: %w", err)
    }
    defer resp.Body.Close()

    if resp.StatusCode != http.StatusOK {
        return nil, fmt.Errorf("github user API: status %d", resp.StatusCode)
    }

    var user GitHubUser
    if err := json.NewDecoder(resp.Body).Decode(&user); err != nil {
        return nil, fmt.Errorf("decoding user: %w", err)
    }
    return &user, nil
}

func randomState() string {
    b := make([]byte, 16)
    rand.Read(b)
    return hex.EncodeToString(b)
}
```

**Step 3: Run tests**

Run: `make test`
Expected: PASS

**Step 4: Commit**

```bash
git add pkg/oauth/
git commit -S -m "feat: add GitHub OAuth web flow"
```

---

### Task 9: Auth Middleware

HTTP middleware that validates session cookies and injects tenant into request context.

**Files:**
- Create: `pkg/middleware/auth.go`
- Create: `pkg/middleware/auth_test.go`

**Step 1: Write the failing test**

```go
package middleware

import (
    "net/http"
    "net/http/httptest"
    "testing"

    "github.com/stretchr/testify/assert"
)

func TestRequireAuth_NoCookie(t *testing.T) {
    handler := RequireAuth(nil, "/auth/github")(
        http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            w.WriteHeader(http.StatusOK)
        }),
    )

    req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
    rec := httptest.NewRecorder()
    handler.ServeHTTP(rec, req)

    assert.Equal(t, http.StatusFound, rec.Code)
    assert.Equal(t, "/auth/github", rec.Header().Get("Location"))
}

func TestTenantFromContext_Missing(t *testing.T) {
    req := httptest.NewRequest(http.MethodGet, "/", nil)
    tn := TenantFromContext(req.Context())
    assert.Nil(t, tn)
}
```

**Step 2: Write the implementation**

```go
package middleware

import (
    "context"
    "database/sql"
    "log/slog"
    "net/http"

    "github.com/mchmarny/devpulse/pkg/tenant"
)

type contextKey string

const tenantContextKey contextKey = "tenant"

func RequireAuth(db *sql.DB, loginURL string) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            cookie, err := r.Cookie("__Host-session")
            if err != nil {
                http.Redirect(w, r, loginURL, http.StatusFound)
                return
            }

            tn, err := tenant.ValidateSession(db, cookie.Value)
            if err != nil {
                slog.Debug("invalid session", "error", err)
                clearSessionCookie(w)
                http.Redirect(w, r, loginURL, http.StatusFound)
                return
            }

            ctx := context.WithValue(r.Context(), tenantContextKey, tn)
            next.ServeHTTP(w, r.WithContext(ctx))
        })
    }
}

func TenantFromContext(ctx context.Context) *tenant.Tenant {
    if t, ok := ctx.Value(tenantContextKey).(*tenant.Tenant); ok {
        return t
    }
    return nil
}

func SetSessionCookie(w http.ResponseWriter, token string, maxAge int) {
    http.SetCookie(w, &http.Cookie{
        Name:     "__Host-session",
        Value:    token,
        Path:     "/",
        MaxAge:   maxAge,
        Secure:   true,
        HttpOnly: true,
        SameSite: http.SameSiteStrictMode,
    })
}

func clearSessionCookie(w http.ResponseWriter) {
    SetSessionCookie(w, "", -1)
}
```

**Step 3: Run tests**

Run: `make test`
Expected: PASS

**Step 4: Commit**

```bash
git add pkg/middleware/
git commit -S -m "feat: add auth middleware with session validation"
```

---

### Task 10: GitHub App JWT and Installation Token Minting

Mint short-lived JWTs from the GitHub App private key and exchange them for installation tokens.

**Files:**
- Create: `pkg/tenant/githubapp.go`
- Create: `pkg/tenant/githubapp_test.go`

**Dependencies:** Add `github.com/golang-jwt/jwt/v5` to `go.mod`.

**Step 1: Write the failing test**

```go
package tenant

import (
    "crypto/rand"
    "crypto/rsa"
    "testing"

    "github.com/stretchr/testify/require"
)

func TestCreateAppJWT(t *testing.T) {
    key, err := rsa.GenerateKey(rand.Reader, 2048)
    require.NoError(t, err)

    cfg := &GitHubAppConfig{
        AppID:      12345,
        PrivateKey: key,
    }

    token, err := CreateAppJWT(cfg)
    require.NoError(t, err)
    require.NotEmpty(t, token)
}
```

**Step 2: Write the implementation**

```go
package tenant

import (
    "context"
    "crypto/rsa"
    "encoding/json"
    "fmt"
    "net/http"
    "time"

    "github.com/golang-jwt/jwt/v5"
)

type GitHubAppConfig struct {
    AppID          int64
    PrivateKey     *rsa.PrivateKey
    InstallBaseURL string // override for testing, default: https://api.github.com
}

type InstallationToken struct {
    Token     string    `json:"token"`
    ExpiresAt time.Time `json:"expires_at"`
}

func CreateAppJWT(cfg *GitHubAppConfig) (string, error) {
    now := time.Now()
    claims := jwt.RegisteredClaims{
        IssuedAt:  jwt.NewNumericDate(now.Add(-60 * time.Second)),
        ExpiresAt: jwt.NewNumericDate(now.Add(10 * time.Minute)),
        Issuer:    fmt.Sprintf("%d", cfg.AppID),
    }

    token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
    signed, err := token.SignedString(cfg.PrivateKey)
    if err != nil {
        return "", fmt.Errorf("signing app JWT: %w", err)
    }
    return signed, nil
}

func MintInstallationToken(ctx context.Context, cfg *GitHubAppConfig, installationID int64) (*InstallationToken, error) {
    appJWT, err := CreateAppJWT(cfg)
    if err != nil {
        return nil, err
    }

    baseURL := cfg.InstallBaseURL
    if baseURL == "" {
        baseURL = "https://api.github.com"
    }

    url := fmt.Sprintf("%s/app/installations/%d/access_tokens", baseURL, installationID)
    req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
    if err != nil {
        return nil, fmt.Errorf("creating installation token request: %w", err)
    }
    req.Header.Set("Authorization", "Bearer "+appJWT)
    req.Header.Set("Accept", "application/vnd.github+json")

    resp, err := http.DefaultClient.Do(req)
    if err != nil {
        return nil, fmt.Errorf("requesting installation token: %w", err)
    }
    defer resp.Body.Close()

    if resp.StatusCode != http.StatusCreated {
        return nil, fmt.Errorf("installation token request: status %d", resp.StatusCode)
    }

    var it InstallationToken
    if err := json.NewDecoder(resp.Body).Decode(&it); err != nil {
        return nil, fmt.Errorf("decoding installation token: %w", err)
    }
    return &it, nil
}
```

**Step 3: Run tests**

Run: `make test`
Expected: PASS

**Step 4: Commit**

```bash
git add pkg/tenant/githubapp.go pkg/tenant/githubapp_test.go go.mod go.sum
git commit -S -m "feat: add GitHub App JWT and installation token minting"
```

---

### Task 11: GitHub App Installation CRUD

Store and manage GitHub App installations and tenant_repo records.

**Files:**
- Create: `pkg/tenant/installation.go`
- Create: `pkg/tenant/installation_test.go`

**Step 1: Write the failing test**

```go
package tenant

import (
    "testing"

    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
)

func TestInstallationCRUD(t *testing.T) {
    db := setupTestDB(t)

    tn, err := UpsertTenant(db, 77777, "installuser", "", "")
    require.NoError(t, err)

    // Save installation
    err = SaveInstallation(db, tn.ID, 9001, "Organization", "myorg", nil)
    require.NoError(t, err)

    // List installations
    installs, err := ListInstallations(db, tn.ID)
    require.NoError(t, err)
    require.Len(t, installs, 1)
    assert.Equal(t, int64(9001), installs[0].InstallationID)
    assert.Equal(t, "myorg", installs[0].TargetLogin)

    // Suspend installation
    err = SuspendInstallation(db, 9001)
    require.NoError(t, err)

    installs, _ = ListInstallations(db, tn.ID)
    require.NotNil(t, installs[0].SuspendedAt)
}

func TestTenantRepoCRUD(t *testing.T) {
    db := setupTestDB(t)

    tn, err := UpsertTenant(db, 77778, "repouser", "", "")
    require.NoError(t, err)

    // Add repos
    err = AddTenantRepos(db, tn.ID, []OrgRepo{
        {Org: "myorg", Repo: "repo1"},
        {Org: "myorg", Repo: "repo2"},
    })
    require.NoError(t, err)

    // List repos
    repos, err := ListTenantRepos(db, tn.ID)
    require.NoError(t, err)
    assert.Len(t, repos, 2)

    // Enforce limit
    tn3, _ := UpsertTenant(db, 77779, "limituser", "", "")
    // max_repos default is 3, try to add 4
    err = AddTenantRepos(db, tn3.ID, []OrgRepo{
        {Org: "o", Repo: "r1"},
        {Org: "o", Repo: "r2"},
        {Org: "o", Repo: "r3"},
        {Org: "o", Repo: "r4"},
    })
    require.Error(t, err) // exceeds max_repos
}
```

**Step 2: Write the implementation**

```go
package tenant

import (
    "database/sql"
    "errors"
    "fmt"
    "time"
)

var errRepoLimitExceeded = errors.New("repo limit exceeded for plan")

type Installation struct {
    ID             string
    TenantID       string
    InstallationID int64
    TargetType     string
    TargetLogin    string
    SuspendedAt    *time.Time
    CreatedAt      time.Time
}

type TenantRepo struct {
    ID       string
    TenantID string
    Org      string
    Repo     string
    Active   bool
}

type OrgRepo struct {
    Org  string
    Repo string
}

const saveInstallationSQL = `
    INSERT INTO github_app_installation (tenant_id, installation_id, target_type, target_login, permissions)
    VALUES ($1, $2, $3, $4, $5)
    ON CONFLICT (installation_id) DO UPDATE SET
        tenant_id = EXCLUDED.tenant_id,
        target_type = EXCLUDED.target_type,
        target_login = EXCLUDED.target_login,
        permissions = EXCLUDED.permissions,
        suspended_at = NULL`

const listInstallationsSQL = `
    SELECT id, tenant_id, installation_id, target_type, target_login, suspended_at, created_at
    FROM github_app_installation WHERE tenant_id = $1 ORDER BY created_at`

const suspendInstallationSQL = `UPDATE github_app_installation SET suspended_at = NOW() WHERE installation_id = $1`

const deleteInstallationSQL = `UPDATE github_app_installation SET suspended_at = NOW() WHERE installation_id = $1`

const addTenantRepoSQL = `
    INSERT INTO tenant_repo (tenant_id, org, repo)
    VALUES ($1, $2, $3)
    ON CONFLICT (tenant_id, org, repo) DO UPDATE SET active = TRUE`

const listTenantReposSQL = `
    SELECT id, tenant_id, org, repo, active
    FROM tenant_repo WHERE tenant_id = $1 AND active = TRUE ORDER BY org, repo`

const countTenantReposSQL = `SELECT COUNT(*) FROM tenant_repo WHERE tenant_id = $1 AND active = TRUE`

const getTenantMaxReposSQL = `SELECT max_repos FROM tenant WHERE id = $1`

func SaveInstallation(db *sql.DB, tenantID string, installationID int64, targetType, targetLogin string, permissions []byte) error {
    _, err := db.Exec(saveInstallationSQL, tenantID, installationID, targetType, targetLogin, permissions)
    if err != nil {
        return fmt.Errorf("saving installation: %w", err)
    }
    return nil
}

func ListInstallations(db *sql.DB, tenantID string) ([]Installation, error) {
    rows, err := db.Query(listInstallationsSQL, tenantID)
    if err != nil {
        return nil, fmt.Errorf("listing installations: %w", err)
    }
    defer rows.Close()

    var result []Installation
    for rows.Next() {
        var i Installation
        if err := rows.Scan(&i.ID, &i.TenantID, &i.InstallationID, &i.TargetType, &i.TargetLogin, &i.SuspendedAt, &i.CreatedAt); err != nil {
            return nil, fmt.Errorf("scanning installation: %w", err)
        }
        result = append(result, i)
    }
    return result, rows.Err()
}

func SuspendInstallation(db *sql.DB, installationID int64) error {
    _, err := db.Exec(suspendInstallationSQL, installationID)
    if err != nil {
        return fmt.Errorf("suspending installation: %w", err)
    }
    return nil
}

func AddTenantRepos(db *sql.DB, tenantID string, repos []OrgRepo) error {
    var maxRepos int
    if err := db.QueryRow(getTenantMaxReposSQL, tenantID).Scan(&maxRepos); err != nil {
        return fmt.Errorf("getting max repos: %w", err)
    }

    var currentCount int
    if err := db.QueryRow(countTenantReposSQL, tenantID).Scan(&currentCount); err != nil {
        return fmt.Errorf("counting repos: %w", err)
    }

    if currentCount+len(repos) > maxRepos {
        return fmt.Errorf("%w: %d + %d > %d", errRepoLimitExceeded, currentCount, len(repos), maxRepos)
    }

    tx, err := db.Begin()
    if err != nil {
        return fmt.Errorf("beginning tx: %w", err)
    }

    for _, r := range repos {
        if _, err := tx.Exec(addTenantRepoSQL, tenantID, r.Org, r.Repo); err != nil {
            _ = tx.Rollback()
            return fmt.Errorf("adding repo %s/%s: %w", r.Org, r.Repo, err)
        }
    }

    return tx.Commit()
}

func ListTenantRepos(db *sql.DB, tenantID string) ([]TenantRepo, error) {
    rows, err := db.Query(listTenantReposSQL, tenantID)
    if err != nil {
        return nil, fmt.Errorf("listing repos: %w", err)
    }
    defer rows.Close()

    var result []TenantRepo
    for rows.Next() {
        var r TenantRepo
        if err := rows.Scan(&r.ID, &r.TenantID, &r.Org, &r.Repo, &r.Active); err != nil {
            return nil, fmt.Errorf("scanning repo: %w", err)
        }
        result = append(result, r)
    }
    return result, rows.Err()
}
```

**Step 3: Run tests**

Run: `make test`
Expected: PASS

**Step 4: Commit**

```bash
git add pkg/tenant/installation.go pkg/tenant/installation_test.go
git commit -S -m "feat: add installation and tenant repo CRUD"
```

---

## Phase 3: RLS and Tenant Scoping

### Task 12: RLS Migration

Add RLS policies and the `set_tenant_id` trigger to enforce tenant isolation at the database level.

**Files:**
- Create: `pkg/data/postgres/sql/migrations_saas/002_rls_policies.sql`

**Step 1: Write the migration**

```sql
-- Enable RLS on global tables (scoped via tenant_repo join)
ALTER TABLE event ENABLE ROW LEVEL SECURITY;
ALTER TABLE developer ENABLE ROW LEVEL SECURITY;
ALTER TABLE repo_meta ENABLE ROW LEVEL SECURITY;
ALTER TABLE release ENABLE ROW LEVEL SECURITY;
ALTER TABLE release_asset ENABLE ROW LEVEL SECURITY;
ALTER TABLE container_package ENABLE ROW LEVEL SECURITY;
ALTER TABLE repo_metric_history ENABLE ROW LEVEL SECURITY;
ALTER TABLE community_health ENABLE ROW LEVEL SECURITY;
ALTER TABLE reputation ENABLE ROW LEVEL SECURITY;

-- RLS policy for org+repo scoped tables: tenant sees data only for tracked repos
CREATE POLICY tenant_repo_filter ON event
    USING (EXISTS (
        SELECT 1 FROM tenant_repo tr
        WHERE tr.tenant_id = current_setting('app.tenant_id', true)::uuid
          AND tr.org = event.org AND tr.repo = event.repo AND tr.active = true
    ));

CREATE POLICY tenant_repo_filter ON repo_meta
    USING (EXISTS (
        SELECT 1 FROM tenant_repo tr
        WHERE tr.tenant_id = current_setting('app.tenant_id', true)::uuid
          AND tr.org = repo_meta.org AND tr.repo = repo_meta.repo AND tr.active = true
    ));

CREATE POLICY tenant_repo_filter ON release
    USING (EXISTS (
        SELECT 1 FROM tenant_repo tr
        WHERE tr.tenant_id = current_setting('app.tenant_id', true)::uuid
          AND tr.org = release.org AND tr.repo = release.repo AND tr.active = true
    ));

CREATE POLICY tenant_repo_filter ON release_asset
    USING (EXISTS (
        SELECT 1 FROM tenant_repo tr
        JOIN release rl ON rl.org = tr.org AND rl.repo = tr.repo
        WHERE tr.tenant_id = current_setting('app.tenant_id', true)::uuid
          AND rl.id = release_asset.release_id AND tr.active = true
    ));

CREATE POLICY tenant_repo_filter ON container_package
    USING (EXISTS (
        SELECT 1 FROM tenant_repo tr
        WHERE tr.tenant_id = current_setting('app.tenant_id', true)::uuid
          AND tr.org = container_package.org AND tr.repo = container_package.repo AND tr.active = true
    ));

CREATE POLICY tenant_repo_filter ON repo_metric_history
    USING (EXISTS (
        SELECT 1 FROM tenant_repo tr
        WHERE tr.tenant_id = current_setting('app.tenant_id', true)::uuid
          AND tr.org = repo_metric_history.org AND tr.repo = repo_metric_history.repo AND tr.active = true
    ));

CREATE POLICY tenant_repo_filter ON community_health
    USING (EXISTS (
        SELECT 1 FROM tenant_repo tr
        WHERE tr.tenant_id = current_setting('app.tenant_id', true)::uuid
          AND tr.org = community_health.org AND tr.repo = community_health.repo AND tr.active = true
    ));

-- Developer table: visible if developer contributed to any tenant repo
CREATE POLICY tenant_developer_filter ON developer
    USING (EXISTS (
        SELECT 1 FROM event e
        JOIN tenant_repo tr ON tr.org = e.org AND tr.repo = e.repo AND tr.active = true
        WHERE tr.tenant_id = current_setting('app.tenant_id', true)::uuid
          AND e.username = developer.username
    ));

-- Reputation: visible if developer has contributions in tenant's repos
CREATE POLICY tenant_reputation_filter ON reputation
    USING (EXISTS (
        SELECT 1 FROM event e
        JOIN tenant_repo tr ON tr.org = e.org AND tr.repo = e.repo AND tr.active = true
        WHERE tr.tenant_id = current_setting('app.tenant_id', true)::uuid
          AND e.username = reputation.username
    ));

-- Enable RLS on tenant-scoped tables
ALTER TABLE tenant_repo ENABLE ROW LEVEL SECURITY;
ALTER TABLE tenant_member ENABLE ROW LEVEL SECURITY;
ALTER TABLE github_app_installation ENABLE ROW LEVEL SECURITY;
ALTER TABLE session ENABLE ROW LEVEL SECURITY;

CREATE POLICY tenant_direct ON tenant_repo
    USING (tenant_id = current_setting('app.tenant_id', true)::uuid);
CREATE POLICY tenant_direct ON tenant_member
    USING (tenant_id = current_setting('app.tenant_id', true)::uuid);
CREATE POLICY tenant_direct ON github_app_installation
    USING (tenant_id = current_setting('app.tenant_id', true)::uuid);
CREATE POLICY tenant_direct ON session
    USING (tenant_id = current_setting('app.tenant_id', true)::uuid);

-- Import state: add tenant_id column (tenant-scoped)
ALTER TABLE import_state ADD COLUMN IF NOT EXISTS tenant_id UUID REFERENCES tenant(id);
ALTER TABLE import_state ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_direct ON import_state
    USING (tenant_id = current_setting('app.tenant_id', true)::uuid);

-- Generated insights: add tenant_id column (tenant-scoped)
ALTER TABLE repo_insights ADD COLUMN IF NOT EXISTS tenant_id UUID REFERENCES tenant(id);
ALTER TABLE repo_insights ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_direct ON repo_insights
    USING (tenant_id = current_setting('app.tenant_id', true)::uuid);
```

Note: `current_setting('app.tenant_id', true)` — the `true` parameter means return NULL (not error) when the setting is missing. This allows admin/import connections that don't set the variable to bypass RLS (they must be the table owner or superuser for this to work).

**Step 2: Commit**

```bash
git add pkg/data/postgres/sql/migrations_saas/002_rls_policies.sql
git commit -S -m "feat: add RLS policies for tenant data isolation"
```

---

### Task 13: Tenant Scope Middleware

Middleware that sets `app.tenant_id` on the PostgreSQL connection for each request, enabling RLS.

**Files:**
- Create: `pkg/middleware/tenant_scope.go`
- Create: `pkg/middleware/tenant_scope_test.go`

**Step 1: Write the failing test**

```go
package middleware

import (
    "net/http"
    "net/http/httptest"
    "testing"
    "time"

    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
    "github.com/mchmarny/devpulse/pkg/tenant"
)

func TestInjectTenantScope(t *testing.T) {
    // This test requires a real DB to verify SET app.tenant_id
    // Use setupTestDB from tenant package or skip for unit test
    t.Skip("integration test — requires PostgreSQL")
}

func TestInjectTenantScope_NoTenant(t *testing.T) {
    handler := InjectTenantScope(nil)(
        http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            w.WriteHeader(http.StatusOK)
        }),
    )

    req := httptest.NewRequest(http.MethodGet, "/", nil)
    rec := httptest.NewRecorder()
    handler.ServeHTTP(rec, req)

    // Should return 401 when no tenant in context
    assert.Equal(t, http.StatusUnauthorized, rec.Code)
}
```

**Step 2: Write the implementation**

```go
package middleware

import (
    "database/sql"
    "fmt"
    "log/slog"
    "net/http"
)

func InjectTenantScope(db *sql.DB) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            tn := TenantFromContext(r.Context())
            if tn == nil {
                http.Error(w, "unauthorized", http.StatusUnauthorized)
                return
            }

            // Set PostgreSQL session variable for RLS
            if db != nil {
                if _, err := db.ExecContext(r.Context(),
                    fmt.Sprintf("SET LOCAL app.tenant_id = '%s'", tn.ID)); err != nil {
                    slog.Error("setting tenant scope", "error", err, "tenant_id", tn.ID)
                    http.Error(w, "internal error", http.StatusInternalServerError)
                    return
                }
            }

            next.ServeHTTP(w, r)
        })
    }
}
```

**Important note:** `SET LOCAL` only works within a transaction. For the HTTP handler path, we need to wrap each request in a transaction, or use a connection-scoped approach. This will be refined in Task 14 when wiring up the actual request flow — the middleware may need to acquire a dedicated connection and use `SET` (session-level) on it.

**Step 3: Run tests**

Run: `make test`
Expected: PASS

**Step 4: Commit**

```bash
git add pkg/middleware/tenant_scope.go pkg/middleware/tenant_scope_test.go
git commit -S -m "feat: add tenant scope middleware for RLS"
```

---

## Phase 4: HTTP Server (OAuth Handlers + Webhook + Dashboard)

### Task 14: Wire Up SaaS HTTP Server

Connect OAuth handlers, webhook endpoint, auth middleware, and reuse existing data API handlers in the `cmd/devpulse-cloud/serve.go`.

**Files:**
- Modify: `cmd/devpulse-cloud/serve.go`
- Create: `cmd/devpulse-cloud/handlers_auth.go`
- Create: `cmd/devpulse-cloud/handlers_webhook.go`
- Create: `cmd/devpulse-cloud/handlers_api.go`

**Step 1: Implement OAuth handlers**

`cmd/devpulse-cloud/handlers_auth.go`:

```go
package main

import (
    "crypto/subtle"
    "log/slog"
    "net/http"
    "time"

    "github.com/mchmarny/devpulse/pkg/middleware"
    "github.com/mchmarny/devpulse/pkg/oauth"
    "github.com/mchmarny/devpulse/pkg/tenant"
)

const sessionTTL = 7 * 24 * time.Hour

func oauthStartHandler(cfg *oauth.GitHubOAuthConfig) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        url, state := oauth.BuildAuthURL(cfg)
        http.SetCookie(w, &http.Cookie{
            Name:     "oauth_state",
            Value:    state,
            Path:     "/",
            MaxAge:   600,
            Secure:   true,
            HttpOnly: true,
            SameSite: http.SameSiteLaxMode, // Lax for OAuth redirect
        })
        http.Redirect(w, r, url, http.StatusFound)
    }
}

func oauthCallbackHandler(db *sql.DB, cfg *oauth.GitHubOAuthConfig) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        // Verify state
        stateCookie, err := r.Cookie("oauth_state")
        if err != nil || subtle.ConstantTimeCompare(
            []byte(stateCookie.Value),
            []byte(r.URL.Query().Get("state")),
        ) != 1 {
            http.Error(w, "invalid state", http.StatusBadRequest)
            return
        }

        // Exchange code for token
        code := r.URL.Query().Get("code")
        token, err := oauth.ExchangeCode(r.Context(), cfg, code)
        if err != nil {
            slog.Error("oauth exchange failed", "error", err)
            http.Error(w, "authentication failed", http.StatusBadRequest)
            return
        }

        // Fetch GitHub user (token discarded after this)
        user, err := oauth.FetchUser(r.Context(), cfg, token)
        if err != nil {
            slog.Error("fetching github user", "error", err)
            http.Error(w, "authentication failed", http.StatusInternalServerError)
            return
        }

        // Upsert tenant
        tn, err := tenant.UpsertTenant(db, user.ID, user.Login, user.Email, user.AvatarURL)
        if err != nil {
            slog.Error("upserting tenant", "error", err)
            http.Error(w, "internal error", http.StatusInternalServerError)
            return
        }

        slog.Info("user signed in", "username", tn.Username, "tenant_id", tn.ID)

        // Create session
        sessionToken, err := tenant.CreateSession(db, tn.ID, sessionTTL)
        if err != nil {
            slog.Error("creating session", "error", err)
            http.Error(w, "internal error", http.StatusInternalServerError)
            return
        }

        middleware.SetSessionCookie(w, sessionToken, int(sessionTTL.Seconds()))

        // Redirect based on ToS status
        if tn.ToSAcceptedAt == nil {
            http.Redirect(w, r, "/tos", http.StatusFound)
            return
        }

        http.Redirect(w, r, "/dashboard", http.StatusFound)
    }
}

func signoutHandler(db *sql.DB) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        if cookie, err := r.Cookie("__Host-session"); err == nil {
            tenant.DestroySession(db, cookie.Value)
        }
        middleware.SetSessionCookie(w, "", -1)
        http.Redirect(w, r, "/", http.StatusFound)
    }
}
```

**Step 2: Implement webhook handler**

`cmd/devpulse-cloud/handlers_webhook.go`:

```go
package main

import (
    "crypto/hmac"
    "crypto/sha256"
    "encoding/hex"
    "encoding/json"
    "fmt"
    "io"
    "log/slog"
    "net/http"
    "strings"

    "github.com/mchmarny/devpulse/pkg/tenant"
)

func webhookHandler(db *sql.DB, webhookSecret string) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        body, err := io.ReadAll(r.Body)
        if err != nil {
            http.Error(w, "bad request", http.StatusBadRequest)
            return
        }

        // Verify HMAC signature
        sig := r.Header.Get("X-Hub-Signature-256")
        if !verifyWebhookSignature(body, sig, webhookSecret) {
            http.Error(w, "invalid signature", http.StatusUnauthorized)
            return
        }

        event := r.Header.Get("X-GitHub-Event")
        slog.Info("webhook received", "event", event)

        switch event {
        case "installation":
            handleInstallationEvent(db, body)
        case "installation_repositories":
            handleInstallationReposEvent(db, body)
        }

        w.WriteHeader(http.StatusOK)
    }
}

func verifyWebhookSignature(payload []byte, signature, secret string) bool {
    if !strings.HasPrefix(signature, "sha256=") {
        return false
    }
    sig, err := hex.DecodeString(strings.TrimPrefix(signature, "sha256="))
    if err != nil {
        return false
    }
    mac := hmac.New(sha256.New, []byte(secret))
    mac.Write(payload)
    return hmac.Equal(sig, mac.Sum(nil))
}

func handleInstallationEvent(db *sql.DB, body []byte) {
    var payload struct {
        Action       string `json:"action"`
        Installation struct {
            ID          int64  `json:"id"`
            Account     struct {
                Login string `json:"login"`
                Type  string `json:"type"`
            } `json:"account"`
        } `json:"installation"`
        Sender struct {
            ID int64 `json:"id"`
        } `json:"sender"`
        Repositories []struct {
            FullName string `json:"full_name"`
        } `json:"repositories"`
    }
    if err := json.Unmarshal(body, &payload); err != nil {
        slog.Error("parsing installation webhook", "error", err)
        return
    }

    slog.Info("installation event",
        "action", payload.Action,
        "installation_id", payload.Installation.ID,
        "sender_id", payload.Sender.ID,
    )

    switch payload.Action {
    case "created":
        // Find tenant by sender GitHub ID
        tn, err := tenant.GetTenantByGitHubID(db, payload.Sender.ID)
        if err != nil {
            slog.Error("tenant not found for installation", "sender_id", payload.Sender.ID, "error", err)
            return
        }

        if err := tenant.SaveInstallation(db, tn.ID, payload.Installation.ID,
            payload.Installation.Account.Type, payload.Installation.Account.Login, nil); err != nil {
            slog.Error("saving installation", "error", err)
            return
        }

        // Auto-add repos
        repos := make([]tenant.OrgRepo, 0, len(payload.Repositories))
        for _, r := range payload.Repositories {
            parts := strings.SplitN(r.FullName, "/", 2)
            if len(parts) == 2 {
                repos = append(repos, tenant.OrgRepo{Org: parts[0], Repo: parts[1]})
            }
        }
        if len(repos) > 0 {
            if err := tenant.AddTenantRepos(db, tn.ID, repos); err != nil {
                slog.Error("adding repos from installation", "error", err)
            }
        }

    case "deleted", "suspend":
        if err := tenant.SuspendInstallation(db, payload.Installation.ID); err != nil {
            slog.Error("suspending installation", "error", err)
        }
    }
}

func handleInstallationReposEvent(db *sql.DB, body []byte) {
    // Handle repositories_added and repositories_removed
    var payload struct {
        Action       string `json:"action"`
        Installation struct {
            ID int64 `json:"id"`
        } `json:"installation"`
        Sender struct {
            ID int64 `json:"id"`
        } `json:"sender"`
        RepositoriesAdded []struct {
            FullName string `json:"full_name"`
        } `json:"repositories_added"`
        RepositoriesRemoved []struct {
            FullName string `json:"full_name"`
        } `json:"repositories_removed"`
    }
    if err := json.Unmarshal(body, &payload); err != nil {
        slog.Error("parsing installation_repositories webhook", "error", err)
        return
    }

    tn, err := tenant.GetTenantByGitHubID(db, payload.Sender.ID)
    if err != nil {
        slog.Error("tenant not found", "sender_id", payload.Sender.ID, "error", err)
        return
    }

    if len(payload.RepositoriesAdded) > 0 {
        repos := make([]tenant.OrgRepo, 0, len(payload.RepositoriesAdded))
        for _, r := range payload.RepositoriesAdded {
            parts := strings.SplitN(r.FullName, "/", 2)
            if len(parts) == 2 {
                repos = append(repos, tenant.OrgRepo{Org: parts[0], Repo: parts[1]})
            }
        }
        if err := tenant.AddTenantRepos(db, tn.ID, repos); err != nil {
            slog.Error("adding repos", "error", err)
        }
    }

    // TODO: handle repositories_removed (deactivate tenant_repo rows)
}
```

**Step 3: Wire up the full serve command**

Update `cmd/devpulse-cloud/serve.go` to build the router with all routes as described in the design (public routes, auth-wrapped data routes reusing `pkg/cli` handlers).

**Step 4: Verify it compiles**

Run: `go build ./cmd/devpulse-cloud/`
Expected: Compiles

**Step 5: Commit**

```bash
git add cmd/devpulse-cloud/
git commit -S -m "feat: wire up SaaS HTTP server with OAuth, webhook, and data API"
```

---

## Phase 5: Import Worker

### Task 15: Tenant Import Worker Loop

Implement the scheduled import worker that iterates active tenants, mints installation tokens, and calls existing Store import methods.

**Files:**
- Modify: `cmd/devpulse-cloud/import_worker.go`
- Create: `pkg/tenant/import.go`

**Step 1: Implement tenant query helpers**

`pkg/tenant/import.go`:

```go
package tenant

import (
    "database/sql"
    "fmt"
)

type ActiveTenant struct {
    ID       string
    Username string
}

type ActiveRepo struct {
    Org  string
    Repo string
}

const getActiveTenantsSQL = `
    SELECT DISTINCT t.id, t.username
    FROM tenant t
    JOIN tenant_repo tr ON tr.tenant_id = t.id
    WHERE tr.active = TRUE AND t.tos_accepted_at IS NOT NULL`

const getActiveInstallationsSQL = `
    SELECT installation_id, target_login
    FROM github_app_installation
    WHERE tenant_id = $1 AND suspended_at IS NULL`

const getActiveReposForInstallSQL = `
    SELECT tr.org, tr.repo
    FROM tenant_repo tr
    JOIN github_app_installation gi ON gi.tenant_id = tr.tenant_id AND gi.target_login = tr.org
    WHERE tr.tenant_id = $1 AND gi.installation_id = $2 AND tr.active = TRUE`

func GetActiveTenants(db *sql.DB) ([]ActiveTenant, error) {
    rows, err := db.Query(getActiveTenantsSQL)
    if err != nil {
        return nil, fmt.Errorf("querying active tenants: %w", err)
    }
    defer rows.Close()

    var tenants []ActiveTenant
    for rows.Next() {
        var t ActiveTenant
        if err := rows.Scan(&t.ID, &t.Username); err != nil {
            return nil, fmt.Errorf("scanning tenant: %w", err)
        }
        tenants = append(tenants, t)
    }
    return tenants, rows.Err()
}

func GetActiveInstallations(db *sql.DB, tenantID string) ([]struct{ ID int64; Login string }, error) {
    rows, err := db.Query(getActiveInstallationsSQL, tenantID)
    if err != nil {
        return nil, fmt.Errorf("querying installations: %w", err)
    }
    defer rows.Close()

    var result []struct{ ID int64; Login string }
    for rows.Next() {
        var inst struct{ ID int64; Login string }
        if err := rows.Scan(&inst.ID, &inst.Login); err != nil {
            return nil, fmt.Errorf("scanning installation: %w", err)
        }
        result = append(result, inst)
    }
    return result, rows.Err()
}

func GetActiveReposForInstall(db *sql.DB, tenantID string, installationID int64) ([]ActiveRepo, error) {
    rows, err := db.Query(getActiveReposForInstallSQL, tenantID, installationID)
    if err != nil {
        return nil, fmt.Errorf("querying repos: %w", err)
    }
    defer rows.Close()

    var repos []ActiveRepo
    for rows.Next() {
        var r ActiveRepo
        if err := rows.Scan(&r.Org, &r.Repo); err != nil {
            return nil, fmt.Errorf("scanning repo: %w", err)
        }
        repos = append(repos, r)
    }
    return repos, rows.Err()
}
```

**Step 2: Implement the import worker loop**

Update `cmd/devpulse-cloud/import_worker.go` with the full import loop as described in the design — iterate tenants, mint tokens per installation, call existing `Store.ImportEvents`, `ImportRepoMeta`, `ImportReleases`, etc.

**Step 3: Run tests**

Run: `make qualify`
Expected: PASS

**Step 4: Commit**

```bash
git add pkg/tenant/import.go cmd/devpulse-cloud/import_worker.go
git commit -S -m "feat: add tenant import worker loop"
```

---

## Phase 6: Infrastructure

### Task 16: Terraform Configuration

Create the full Terraform configuration for the SaaS GCP project.

**Files:**
- Create: `infra/saas/main.tf`
- Create: `infra/saas/variables.tf`
- Create: `infra/saas/project.tf`
- Create: `infra/saas/network.tf`
- Create: `infra/saas/database.tf`
- Create: `infra/saas/secrets.tf`
- Create: `infra/saas/cloudrun.tf`
- Create: `infra/saas/scheduler.tf`
- Create: `infra/saas/iam.tf`
- Create: `infra/saas/dns.tf`
- Create: `infra/saas/monitoring.tf`
- Create: `infra/saas/outputs.tf`

This task is infrastructure-only. Each file should follow the layout described in the design doc.

Key configuration:
- **Backend**: GCS bucket `devpulse-saas-tf-state`
- **Cloud SQL**: `db-f1-micro`, private IP, SSL enforced, automated backups
- **Cloud Run service**: min instances 0, env vars for `DATABASE_URL`, `GITHUB_APP_*`, `GITHUB_OAUTH_*`
- **Cloud Run job**: `devpulse-cloud import`, triggered by Cloud Scheduler hourly
- **Service accounts**: separate for service (`devpulse-cloud-sa`) and job (`devpulse-import-sa`)
- **Secret Manager**: `github-app-private-key`, `github-oauth-client-secret`, `webhook-secret`
- **DNS**: Cloud DNS zone for `devpulse.thingz.io`

**Step 1: Write each .tf file**

Implementation details depend on GCP project ID and billing account — these go in `variables.tf` with no defaults for sensitive values.

**Step 2: Validate**

Run: `cd infra/saas && terraform init && terraform validate`
Expected: Valid configuration

**Step 3: Commit**

```bash
git add infra/saas/
git commit -S -m "feat: add Terraform configuration for SaaS infrastructure"
```

---

### Task 17: GitHub Actions CI/CD for devpulse-cloud

Add a workflow that builds and deploys the `devpulse-cloud` image to Cloud Run.

**Files:**
- Create: `.github/workflows/deploy-saas.yaml`

This should:
1. Build `devpulse-cloud` container via ko or goreleaser
2. Push to GHCR (or directly to Artifact Registry)
3. Deploy to Cloud Run service + update Cloud Run job
4. Run only on version tags or manually

**Step 1: Write the workflow**

Follow existing patterns from `release-on-tag.yaml` but target the `devpulse-cloud` build.

**Step 2: Commit**

```bash
git add .github/workflows/deploy-saas.yaml
git commit -S -m "ci: add SaaS deployment workflow"
```

---

## Phase 7: Integration Testing and Verification

### Task 18: End-to-End Integration Test

Write an integration test that exercises the full flow: create tenant, create session, add repos, verify RLS isolation between tenants.

**Files:**
- Create: `pkg/tenant/integration_test.go`

**Step 1: Write the test**

```go
package tenant

import (
    "testing"

    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
)

func TestTenantIsolation(t *testing.T) {
    if testing.Short() {
        t.Skip("skipping integration test")
    }

    db := setupTestDB(t)

    // Create two tenants
    t1, err := UpsertTenant(db, 1001, "tenant1", "", "")
    require.NoError(t, err)
    t2, err := UpsertTenant(db, 1002, "tenant2", "", "")
    require.NoError(t, err)

    // Add different repos to each
    err = AddTenantRepos(db, t1.ID, []OrgRepo{{Org: "org1", Repo: "repo1"}})
    require.NoError(t, err)
    err = AddTenantRepos(db, t2.ID, []OrgRepo{{Org: "org2", Repo: "repo2"}})
    require.NoError(t, err)

    // Set tenant scope to t1 and verify they only see their repos
    _, err = db.Exec("SET app.tenant_id = $1", t1.ID)
    require.NoError(t, err)

    repos1, err := ListTenantRepos(db, t1.ID) // RLS should filter
    require.NoError(t, err)
    assert.Len(t, repos1, 1)
    assert.Equal(t, "repo1", repos1[0].Repo)

    // Reset and set to t2
    _, err = db.Exec("RESET app.tenant_id")
    require.NoError(t, err)
    _, err = db.Exec("SET app.tenant_id = $1", t2.ID)
    require.NoError(t, err)

    repos2, err := ListTenantRepos(db, t2.ID)
    require.NoError(t, err)
    assert.Len(t, repos2, 1)
    assert.Equal(t, "repo2", repos2[0].Repo)
}
```

**Step 2: Run tests**

Run: `make test`
Expected: PASS

**Step 3: Commit**

```bash
git add pkg/tenant/integration_test.go
git commit -S -m "test: add tenant isolation integration test"
```

---

### Task 19: Verify Existing CLI Is Untouched

Confirm the self-hosted CLI still builds and all existing tests pass.

**Step 1: Build CLI**

Run: `go build ./cmd/devpulse/`
Expected: Compiles without error

**Step 2: Run full test suite**

Run: `make qualify`
Expected: All tests pass, no lint errors, no vulnerabilities

**Step 3: Verify goreleaser**

Run: `goreleaser build --snapshot --single-target`
Expected: Both `devpulse` and `devpulse-cloud` binaries produced

---

### Task 20: Final Commit and Documentation Update

Update the project CLAUDE.md with the new architecture info and commit everything.

**Files:**
- Modify: `.claude/CLAUDE.md` — add SaaS-specific sections

Add to Architecture section:

```
cmd/devpulse-cloud/     SaaS entrypoint (serve, import subcommands)
pkg/tenant/             Tenant management, GitHub App, sessions
pkg/middleware/         Auth middleware, tenant scope injection
pkg/oauth/             GitHub OAuth web flow
infra/saas/            Terraform for SaaS GCP infrastructure
```

**Step 1: Update CLAUDE.md**

**Step 2: Run final qualify**

Run: `make qualify`
Expected: PASS

**Step 3: Commit**

```bash
git add .claude/CLAUDE.md
git commit -S -m "docs: update architecture for multi-tenant SaaS"
```

---

## Manual Steps (Not Automated)

These must be done by hand before first deployment:

1. **Register GitHub OAuth App** at github.com/settings/applications/new
   - Homepage URL: `https://devpulse.thingz.io`
   - Callback URL: `https://devpulse.thingz.io/auth/github/callback`
   - Note the Client ID and Client Secret

2. **Register GitHub App** at github.com/settings/apps/new
   - Name: `DevPulse`
   - Homepage URL: `https://devpulse.thingz.io`
   - Webhook URL: `https://devpulse.thingz.io/webhook/github`
   - Permissions: Repository (read), Metadata (read)
   - Subscribe to events: Installation, Installation repositories
   - Generate and download private key
   - Note the App ID

3. **Create GCP project** and link billing
4. **Create GCS bucket** for Terraform state: `devpulse-saas-tf-state`
5. **Run `terraform apply`** to provision infrastructure
6. **Store secrets** in Secret Manager (GitHub App key, OAuth client secret, webhook secret)
7. **Delegate DNS** — point `devpulse.thingz.io` NS records to Cloud DNS
8. **Write Terms of Service** — required before launch
9. **Deploy first image** via GitHub Actions

## Task Summary

| Phase | Tasks | Description |
|-------|-------|-------------|
| 1 | 1-7 | Foundation: migrations, tenant CRUD, sessions, entrypoint, goreleaser |
| 2 | 8-11 | Auth: OAuth flow, middleware, GitHub App, installations |
| 3 | 12-13 | RLS: policies, tenant scope middleware |
| 4 | 14 | HTTP server: wire up all routes |
| 5 | 15 | Import worker: tenant iteration loop |
| 6 | 16-17 | Infrastructure: Terraform, CI/CD |
| 7 | 18-20 | Testing and verification |
