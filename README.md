# DevPulse

[![Build Status](https://github.com/thingzio/devpulse/actions/workflows/test-on-push.yaml/badge.svg)](https://github.com/thingzio/devpulse/actions/workflows/test-on-push.yaml)
[![Go Report Card](https://goreportcard.com/badge/github.com/thingzio/devpulse)](https://goreportcard.com/report/github.com/thingzio/devpulse)
[![License: Apache 2.0](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](LICENSE)

Health analytics for GitHub projects.

DevPulse imports a repository's contribution history — pull requests, reviews,
issues, comments, forks, releases — and turns it into the picture that is hard
to get from the GitHub UI: who is actually carrying the project, whether
activity is growing or thinning, how long reviews take, and where the bus factor
sits. Optionally it runs the result past an LLM for a written summary.

> **This is a reference implementation, not a product.** Apache-2.0, self-hostable,
> maintained on a best-effort basis. There are no plans, no pricing, and no SLA.
> See [CONTRIBUTING.md](CONTRIBUTING.md#project-governance) for what that means
> in practice.

A demo instance runs at [devpulse.thingz.io](https://devpulse.thingz.io). It is
this code with the maintainer's data in it, not a commercial service — useful
for seeing the output before running your own.

## Running it locally

You need Go (the version in `go.mod`), Docker, and `make`. You do **not** need a
Google Cloud account or any credential from the maintainer.

```shell
git clone https://github.com/thingzio/devpulse && cd devpulse
make db-up      # local Postgres via docker compose
make server     # run the dashboard on :8080
make import     # run the import worker once (needs GITHUB_TOKEN)
```

Then open <http://localhost:8080>.

To run the full quality gate — the same one CI runs:

```shell
make qualify    # coverage, lint, govulncheck, integration, e2e
```

`make help` lists every target.

> The local Postgres binds port **5432**. DevTrace and DevRadar do the same, so
> only run one of the three stacks at a time or you will get a confusing bind
> failure.

## How it works

Two binaries with independent lifecycles:

```
cmd/devpulse-site/     HTTP server: dashboard, OAuth, webhooks, data API, /admin
cmd/devpulse-import/   Batch import worker, run on a schedule
pkg/importer/          Sharded import pipeline with goroutine workers
pkg/plan/              Per-account limits, to keep a shared instance responsive
pkg/data/postgres/     PostgreSQL store, migrations, row-level security
pkg/tenant/            Accounts, sessions, GitHub App installations
```

Tenant isolation is enforced in the database with PostgreSQL row-level security
rather than in application code: each request acquires a dedicated connection
and sets `app.tenant_id`, and the policies do the rest. Import runs as a
separate job so a slow backfill cannot affect request latency.

## Configuration

Everything is environment variables. `DATABASE_URL` is the only one required to
boot.

| Variable | Purpose |
|---|---|
| `DATABASE_URL` | PostgreSQL connection URI — **required** |
| `BASE_URL` | Public base URL of this instance |
| `GITHUB_TOKEN` | Personal access token, enough for the import worker alone |
| `GITHUB_OAUTH_CLIENT_ID` / `_SECRET` | GitHub OAuth app, for sign-in |
| `GITHUB_APP_ID` / `GITHUB_APP_KEY_PATH` | GitHub App, for installation tokens |
| `DEVPULSE_ADMIN_USERS` | Comma-separated GitHub usernames granted `/admin` |
| `ANTHROPIC_API_KEY` | Optional; enables LLM-generated insights |
| `DEVPULSE_DEBUG` | `true` for debug-level logging |

Self-hosters bring their own keys, so AI cost scales to whoever runs the
instance. The full list is in [docs/DEVELOPMENT.md](docs/DEVELOPMENT.md).

**Running a full instance requires registering your own GitHub App** — App
private keys are per-instance and cannot be shared. A plain `GITHUB_TOKEN` is
enough to exercise the import worker without one.

## Deploying

The maintainer's deployment runs on Google Cloud — Cloud Run, Cloud SQL,
Secret Manager, Cloud Scheduler — provisioned by Terraform that lives in a
private repository, because it names every service account, secret and resource
in production.

[docs/INFRASTRUCTURE.md](docs/INFRASTRUCTURE.md) describes that architecture,
its scaling behaviour and its actual costs. It is a reference for how this is
run, not a turnkey installer: the application itself is a plain Go binary and a
Postgres database, and nothing here is specific to GCP.

## Documentation

| Doc | Purpose |
|---|---|
| [DEVELOPMENT.md](docs/DEVELOPMENT.md) | Local setup, make targets, debugging |
| [ARCHITECTURE.md](docs/ARCHITECTURE.md) | System design, data model, tenant isolation |
| [INFRASTRUCTURE.md](docs/INFRASTRUCTURE.md) | Scaling, running costs, API throughput |
| [ADMIN.md](docs/ADMIN.md) | Day-2 operations, monitoring, account management |
| [PERFORMANCE.md](docs/PERFORMANCE.md) | Query tuning method and endpoint baselines |
| [VIEWS.md](docs/VIEWS.md) | Dashboard tabs and chart guide |
| [GITLAB.md](docs/GITLAB.md) | GitLab support feasibility assessment |

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Documentation fixes are especially
welcome and are the easiest first contribution.

Security reports go through
[GitHub Security Advisories](https://github.com/thingzio/devpulse/security/advisories/new),
not public issues — see [SECURITY.md](SECURITY.md).

## License

[Apache 2.0](LICENSE). See [NOTICE](NOTICE) for attribution.
