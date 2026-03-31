# devpulse

[![Build Status](https://github.com/thingzio/devpulse/actions/workflows/test-on-push.yaml/badge.svg)](https://github.com/thingzio/devpulse/actions/workflows/test-on-push.yaml)
[![Go Report Card](https://goreportcard.com/badge/github.com/thingzio/devpulse)](https://goreportcard.com/report/github.com/thingzio/devpulse)
[![License: Apache 2.0](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](LICENSE)

Multi-tenant SaaS for GitHub project health analytics. Users sign in with GitHub OAuth, install a GitHub App on their repos, and get a hosted dashboard at [devpulse.thingz.io](https://devpulse.thingz.io).

Three separate binaries: `devpulse-site` (HTTP server), `devpulse-import` (batch worker), `devpulse-admin` (tenant management). PostgreSQL with Row-Level Security for tenant isolation.

## Development

```shell
git clone https://github.com/thingzio/devpulse.git && cd devpulse
make test       # unit tests with race detector
make lint       # go vet + golangci-lint + tfsec
make qualify    # test-coverage + lint + govulncheck + e2e
```

## Documentation

| Doc | Purpose |
|-----|---------|
| [DEVELOPMENT.md](docs/DEVELOPMENT.md) | Local setup, make targets, debugging, operations, CI/CD |
| [ARCHITECTURE.md](docs/ARCHITECTURE.md) | System design, data model, tenant isolation |
| [INFRASTRUCTURE.md](docs/INFRASTRUCTURE.md) | GCP tiers, scaling, costs, API throughput |
| [BOOTSTRAP.md](docs/BOOTSTRAP.md) | One-time GCP deployment guide |
| [MONITORING.md](docs/MONITORING.md) | Alerts, log metrics, dashboards |

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for guidelines and [docs/DEVELOPMENT.md](docs/DEVELOPMENT.md) for setup.

1. Fork and clone
2. Create a feature branch
3. Run `make qualify`
4. Submit a pull request

## License

[Apache 2.0](LICENSE)
