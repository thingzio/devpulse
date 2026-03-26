# devpulse

[![Build Status](https://github.com/thingzio/devpulse/actions/workflows/test-on-push.yaml/badge.svg)](https://github.com/thingzio/devpulse/actions/workflows/test-on-push.yaml)
[![Go Report Card](https://goreportcard.com/badge/github.com/thingzio/devpulse)](https://goreportcard.com/report/github.com/thingzio/devpulse)
[![License: Apache 2.0](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](LICENSE)

Multi-tenant SaaS service for GitHub project health analytics. Imports contribution data via GitHub App, computes health metrics (bus factor, velocity, review latency, contributor retention), and serves a dashboard at [devpulse.thingz.io](https://devpulse.thingz.io).

## Development

```shell
git clone https://github.com/thingzio/devpulse.git && cd devpulse
make test       # unit tests with race detector
make lint       # go vet + golangci-lint
make qualify    # test + lint + govulncheck + e2e
make build      # goreleaser single-target build
```

See [DEVELOPMENT.md](DEVELOPMENT.md) for prerequisites, make targets, and debugging.

## Architecture

Two binaries from one repo:

| Binary | Purpose | Deploy |
|--------|---------|--------|
| `devpulse` | CLI for local data import and dashboard | Container image on Cloud Run |
| `devpulse-cloud` | Multi-tenant SaaS server + import worker | Container image on Cloud Run |

Key packages:

```
cmd/devpulse/           CLI entrypoint
cmd/devpulse-cloud/     SaaS entrypoint (serve, import subcommands)
pkg/cli/                CLI commands, HTTP handlers, templates, static assets
pkg/data/               Store interface, SQLite + PostgreSQL implementations
pkg/tenant/             Tenant CRUD, sessions, GitHub App, installations
pkg/middleware/         Auth middleware, tenant scope (RLS)
pkg/oauth/              GitHub OAuth web flow
infra/saas/             Terraform for SaaS GCP infrastructure
```

Tenant isolation uses PostgreSQL Row-Level Security. See [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) for details.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for guidelines and [DEVELOPMENT.md](DEVELOPMENT.md) for setup.

1. Fork and clone
2. Create a feature branch
3. Run `make qualify`
4. Submit a pull request

## License

[Apache 2.0](LICENSE)
