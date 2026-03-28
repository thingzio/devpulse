# devpulse

[![Build Status](https://github.com/thingzio/devpulse/actions/workflows/test-on-push.yaml/badge.svg)](https://github.com/thingzio/devpulse/actions/workflows/test-on-push.yaml)
[![Go Report Card](https://goreportcard.com/badge/github.com/thingzio/devpulse)](https://goreportcard.com/report/github.com/thingzio/devpulse)
[![License: Apache 2.0](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](LICENSE)

Multi-tenant SaaS for GitHub project health analytics. Users sign in with GitHub OAuth, install a GitHub App on their repos, and get a hosted dashboard at [devpulse.thingz.io](https://devpulse.thingz.io).

## Development

```shell
git clone https://github.com/thingzio/devpulse.git && cd devpulse
make test       # unit tests with race detector
make lint       # go vet + golangci-lint
make qualify    # test + lint + govulncheck + e2e
```

See [docs/DEVELOPMENT.md](docs/DEVELOPMENT.md) for prerequisites, make targets, and debugging.

## Architecture

Single binary with two subcommands:

| Subcommand | Purpose | Deploy |
|------------|---------|--------|
| `serve` | HTTP server: dashboard, API, OAuth, webhooks | Cloud Run service |
| `import` | Scheduled tenant data import worker | Cloud Run job (hourly) |

Key packages:

```
cmd/devpulse/       Entrypoint (serve, import subcommands)
pkg/tenant/         Tenant CRUD, sessions, GitHub App, installations
pkg/middleware/     Auth middleware, tenant scope (RLS)
pkg/oauth/          GitHub OAuth web flow
pkg/data/           Store interface, PostgreSQL implementation
pkg/data/postgres/  Queries, migrations (base + SaaS)
pkg/data/ghutil/    GitHub API helpers (rate limiting, user mapping)
infra/saas/         Terraform for GCP infrastructure
```

Tenant isolation uses PostgreSQL Row-Level Security. See [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) for details.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for guidelines and [docs/DEVELOPMENT.md](docs/DEVELOPMENT.md) for setup.

1. Fork and clone
2. Create a feature branch
3. Run `make qualify`
4. Submit a pull request

## License

[Apache 2.0](LICENSE)
