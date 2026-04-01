# Development Guide

## Quick Start

```bash
git clone https://github.com/thingzio/devpulse.git && cd devpulse
make up             # start local Postgres
make server         # run HTTP server on :8080
make import         # run import worker (needs GITHUB_TOKEN)
make qualify        # full check: test-coverage + lint + vulncheck + e2e
```

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
make up             # start Postgres (data persists in pgdata volume)
make server         # runs devpulse-site on :8080
make import         # runs devpulse-import, processes tenants and exits
make stats          # show tenant count, repos, recent sign-ins
make db             # open psql shell
make down           # stop Postgres (data preserved)
```

### Mode Selection

`make server` runs `devpulse-site` (HTTP server on :8080). `make import` runs `devpulse-import` (batch worker, exits when done). `devpulse-admin` is an IAM-protected admin service for tenant management — use `./tools/tenant-upgrade <username> <plan>` in production.

Debug logging: set `DEVPULSE_DEBUG=true` (always JSON format).

## Make Targets

### Local Development

| Target | Description |
|--------|-------------|
| `make up` | Start local Postgres (docker compose) |
| `make down` | Stop local Postgres |
| `make server` | Run HTTP server on :8080 |
| `make import` | Run import worker |
| `make db` | Open psql shell to local Postgres |
| `make stats` | Show tenant and repo stats |

### Quality

| Target | Description |
|--------|-------------|
| `make qualify` | Full qualification (test-coverage + lint + vulncheck + e2e) |
| `make test` | Unit tests with race detector and coverage |
| `make test-coverage` | Tests with coverage threshold enforcement |
| `make lint` | Go + YAML + Terraform linting |
| `make vulncheck` | Vulnerability scanning with govulncheck |
| `make e2e` | End-to-end tests |

### Build & Release

| Target | Description |
|--------|-------------|
| `make build` | Build binary for current OS/arch (output in `./dist`) |
| `make release` | Full release with goreleaser (snapshot) |
| `make bump-patch` | Bump patch version and push tag |
| `make bump-minor` | Bump minor version and push tag |
| `make bump-major` | Bump major version and push tag |

Pushing a version tag triggers the CI release workflow (goreleaser build, container image push, Cloud Run deploy).

### Maintenance

| Target | Description |
|--------|-------------|
| `make tidy` | Format code, tidy modules, vendor dependencies |
| `make upgrade` | Upgrade all dependencies to latest |
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
| `make server` fails | Ensure Postgres is running (`make up`) and `DATABASE_URL` is set |

### Running Specific Tests

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

## CI/CD

GitHub Actions workflows in `.github/workflows/`:

| Workflow | Trigger | Purpose |
|----------|---------|---------|
| `test-on-push.yaml` | push to main, PRs | Calls reusable test workflow |
| `test-on-call.yaml` | reusable (workflow_call) | tidy, lint, test with race detector |
| `release-on-tag.yaml` | version tags (`v*.*.*`) | goreleaser build, container image push, Cloud Run deploy |
| `deploy-saas.yaml` | manual (workflow_dispatch) | Deploy devpulse to Cloud Run |

Supply chain: container images built via ko, pushed to GHCR (`ghcr.io/thingzio/devpulse`). govulncheck in CI. All GitHub Actions pinned by commit hash. Tool versions centralized in `.settings.yaml`.

## Local Monitoring

```shell
make stats
```

Shows tenant count, active repos, imported repos, total events, recent sign-ins, and repos per tenant from local Postgres.

All logs are JSON (via `log/slog`). Key messages to watch:

| Message | When |
|---------|------|
| `import worker starting` | Import begins |
| `claimed repo` | Repo claimed from queue |
| `repo import complete` | Per-repo done |
| `import worker complete` | Full run done |

## Operations

### Releases

```shell
make bump-patch  # or bump-minor, bump-major
```

Pushing a version tag triggers the CI release pipeline (test → build → push → deploy).

### Manual deploy

```shell
gh workflow run deploy-saas.yaml -f image_tag=v1.2.3
```

### Infrastructure changes

```shell
make tf-plan
make tf-apply
```

### Tenant plan management

Use the admin service or direct SQL:

```sql
-- View tenants
SELECT username, plan, max_repos, max_events_per_week, created_at
FROM tenant ORDER BY created_at;

-- Promote to pro
UPDATE tenant
SET plan = 'pro', max_repos = 15, max_events_per_week = 15000, updated_at = NOW()
WHERE username = 'their-github-username';
```

Plan limits are defined in `pkg/plan/plan.go` (single source of truth):

| Plan | Repos | Events/Week |
|------|-------|-------------|
| free | 3 | 1,000 |
| pro | 15 | 15,000 |
| enterprise | Unlimited | Unlimited |

## Related Documentation

- [README.md](../README.md) — project overview
- [CONTRIBUTING.md](../CONTRIBUTING.md) — contribution guidelines
- [ARCHITECTURE.md](ARCHITECTURE.md) — system architecture and design
- [INFRASTRUCTURE.md](INFRASTRUCTURE.md) — GCP scaling, costs, API throughput
- [BOOTSTRAP.md](BOOTSTRAP.md) — one-time GCP deployment guide
- [MONITORING.md](MONITORING.md) — alerts, log metrics, dashboards
