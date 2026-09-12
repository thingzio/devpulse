# Development Guide

## Quick Start

```bash
git clone https://github.com/thingzio/devpulse.git && cd devpulse
make db-up          # start local Postgres
make server         # run HTTP server on :8080
make import         # run import worker (needs GITHUB_TOKEN)
make qualify        # full check: test-coverage + lint + vulncheck + e2e
```

> **Start here:** Read [`.claude/CLAUDE.md`](../.claude/CLAUDE.md) for coding conventions, anti-patterns, and the decision framework used across the codebase.

## Prerequisites

### Required

| Tool | Purpose | Installation |
|------|---------|--------------|
| **Go 1.26+** | Language runtime | [golang.org/dl](https://golang.org/dl/) |
| **Docker** | Local Postgres via docker-compose | [docker.com](https://docs.docker.com/get-docker/) |
| **make** | Build automation | Pre-installed on macOS; `apt install make` on Ubuntu/Debian |

### Development Tools

| Tool | Purpose | Installation |
|------|---------|--------------|
| golangci-lint | Go linting | [golangci-lint.run](https://golangci-lint.run/welcome/install/) |
| yamllint | YAML linting | `pip install yamllint` |
| govulncheck | Vulnerability scanning | `go install golang.org/x/vuln/cmd/govulncheck@latest` |
| goreleaser | Release builds | [goreleaser.com](https://goreleaser.com/install/) |
| yq | YAML processing | [github.com/mikefarah/yq](https://github.com/mikefarah/yq) |
| psql | Database shell | Included with PostgreSQL client tools |

Tool versions and quality thresholds are centralized in `.settings.yaml`.

## Local Development

### Environment

All configuration is via env vars. The Makefile defaults to the local docker-compose Postgres:

```
DATABASE_URL=postgres://devpulse:devpulse@localhost:5432/devpulse?sslmode=disable
```

For OAuth sign-in, set these before running `make server`:

```bash
export GITHUB_OAUTH_CLIENT_ID="your-client-id"       # localhost OAuth app
export GITHUB_OAUTH_CLIENT_SECRET="your-client-secret"
export BASE_URL="http://localhost:8080"
```

For repo-add gating and import (GitHub App installation token minting):

```bash
export GITHUB_APP_ID="your-app-id"
export GITHUB_APP_KEY_PATH="/path/to/devpulse-app.pem"
```

For the import worker (alternative to GitHub App tokens):

```bash
export GITHUB_TOKEN="ghp_..."
```

For LLM insights generation (optional):

```bash
export ANTHROPIC_API_KEY="sk-ant-..."
export ANTHROPIC_MODEL="claude-haiku-4-5-20251001"   # optional, this is the default
export ANTHROPIC_BASE_URL="https://..."               # optional, custom endpoint
```

### Workflow

```bash
make db-up          # start Postgres (data persists in pgdata volume)
make server         # runs devpulse-site on :8080
make import         # runs devpulse-import, processes tenants and exits
make db-connect     # open psql shell
make db-down        # stop Postgres (data preserved)
```

### Mode Selection

`make server` runs `devpulse-site` (HTTP server on :8080, includes admin dashboard at `/admin`). `make import` runs `devpulse-import` (batch worker, exits when done).

The import binary runs the full pipeline: events, reputation, and LLM insights in a single pass. See [ARCHITECTURE.md](ARCHITECTURE.md) for details.

Debug logging: set `DEVPULSE_DEBUG=true` (always JSON format).

## Make Targets

### Local Development

| Target | Description |
|--------|-------------|
| `make db-up` | Start local Postgres (docker compose) |
| `make db-down` | Stop local Postgres |
| `make db-connect` | Open psql shell to local Postgres |
| `make server` | Run HTTP server on :8080 |
| `make import` | Run import worker |

### Quality

| Target | Description |
|--------|-------------|
| `make qualify` | Full qualification (test-coverage + lint + vulncheck + e2e) |
| `make test` | Unit tests with race detector and coverage |
| `make test-coverage` | Tests with coverage threshold enforcement |
| `make lint` | Go + YAML + Terraform linting |
| `make vulncheck` | Vulnerability scanning with govulncheck |
| `make e2e` | End-to-end tests (via `tools/e2e`) |

### Build & Release

| Target | Description |
|--------|-------------|
| `make build` | Build binary for current OS/arch (output in `./dist`) |
| `make release` | Full release with goreleaser (snapshot) |
| `make bump-patch` | Bump patch version and push tag |
| `make bump-minor` | Bump minor version and push tag |
| `make bump-major` | Bump major version and push tag |

Pushing a version tag triggers the CI release workflow (goreleaser build, container image push, Cloud Run deploy).

### Infrastructure

| Target | Description |
|--------|-------------|
| `make tf-init` | Initialize Terraform |
| `make tf-plan` | Plan Terraform changes |
| `make tf-apply` | Apply Terraform changes |

### Maintenance

| Target | Description |
|--------|-------------|
| `make tidy` | Format code, tidy modules, vendor dependencies |
| `make upgrade` | Upgrade all dependencies to latest |
| `make setup` | Validate and install local dev tool dependencies |
| `make clean` | Clean build artifacts |
| `make clean-all` | Deep clean including Go module cache |
| `make info` | Print version, commit, branch, Go version, linter version |
| `make help` | Show all available targets |

## Debugging

### Common Issues

| Issue | Solution |
|-------|----------|
| Tests fail with race conditions | Check for shared state in goroutines |
| Linter errors | Run `make lint` and fix reported issues |
| Build failures | Run `make tidy` to update dependencies |
| Import hits rate limit | Re-run; the importer uses jitter backoff automatically |
| `make server` fails | Ensure Postgres is running (`make db-up`) and `DATABASE_URL` is set |
| Postgres fails to bind port 5432 | A sibling stack is already up. DevPulse, DevTrace, and DevRadar all bind 5432 in their own compose files, so only one can run at a time. `docker compose down` in whichever is running. The bind error does not say which project holds the port |
| `make integration` fails with `rootless Docker not found` | You are not on Docker Desktop — see [Docker runtimes other than Docker Desktop](#docker-runtimes-other-than-docker-desktop) |
| `make tidy` fails with `inconsistent vendoring` | Run `go mod vendor` first. `make tidy` starts with `go fmt ./...`, which refuses to run while `go.mod` and `vendor/modules.txt` disagree, so it cannot recover from that state on its own |

### Docker runtimes other than Docker Desktop

`make integration` uses [testcontainers-go], which starts a throwaway Postgres per
package. Unlike the `docker` CLI, testcontainers does **not** read Docker
contexts — it probes a fixed set of socket paths. On Colima, Podman, Rancher
Desktop, or anything else that puts the socket elsewhere, it fails with an
error that does not mention the real problem:

```
failed to start postgres container: ... rootless Docker not found,
failed to create Docker provider
```

Point it at your socket and disable the reaper, which tries to bind-mount the
host socket path into a container and cannot on a VM-backed runtime:

```shell
export DOCKER_HOST="unix://$HOME/.colima/default/docker.sock"   # adjust for your runtime
export TESTCONTAINERS_DOCKER_SOCKET_OVERRIDE=/var/run/docker.sock
export TESTCONTAINERS_RYUK_DISABLED=true
```

`docker context inspect --format '{{.Endpoints.docker.Host}}'` prints the socket
path to use. `make e2e` and `make db-up` are unaffected — they go through
`docker compose`, which does read contexts.

[testcontainers-go]: https://golang.testcontainers.org

### Testing Patterns

- **Integration tests** use `setupTestDB(t)` which creates a temporary Postgres container with all migrations applied. These require Docker.
- **Never hardcode dates in fixtures.** Every query taking a `days` argument
  filters through `sinceDate()`, which is relative to `time.Now()`. A fixture
  pinned to a literal date silently ages out of the window and the test starts
  failing on a calendar boundary rather than on a code change. Use the
  `daysAgo(n)` helper in `postgres_test.go`, which shares `sinceDate`'s clock
  and UTC basis so a fixture and the window selecting it cannot disagree.
- **All test functions** must create `ctx := context.Background()` and pass it to Store methods — never use a bare `nil` context.
- **Table-driven tests** are preferred. Test both nil DB and empty DB cases for query functions.
- **Assertions** via `github.com/stretchr/testify` (`assert` for non-fatal, `require` for fatal).

```bash
go test -v ./pkg/tenant/... -run TestSpecificFunction
go test -race ./...
go test -coverprofile=cover.out ./...
go tool cover -html=cover.out
```

### Debug Logging

```bash
# Via make (already sets DEVPULSE_DEBUG=true)
make server

# Directly
DEVPULSE_DEBUG=true DATABASE_URL="postgres://..." go run ./cmd/devpulse-site
```

All logs are JSON to stderr. Use `jq` for local filtering:

```bash
make server 2>&1 | jq 'select(.level=="ERROR")'
```

## Related Documentation

- [README.md](../README.md) — project overview
- [CONTRIBUTING.md](../CONTRIBUTING.md) — contribution guidelines
- [ARCHITECTURE.md](ARCHITECTURE.md) — system architecture and design
- [ADMIN.md](ADMIN.md) — day-2 operations, monitoring, tenant management
- [INFRASTRUCTURE.md](INFRASTRUCTURE.md) — GCP scaling, costs, API throughput
- [PLANS.md](PLANS.md) — plan feature gating (Free, Starter, Pro, Enterprise)
- [BOOTSTRAP.md](BOOTSTRAP.md) — one-time GCP deployment guide
- [`.claude/CLAUDE.md`](../.claude/CLAUDE.md) — coding conventions, anti-patterns, decision framework
