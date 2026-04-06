# GitLab Support Assessment

Status: **Research / Not Started**

This document evaluates the feasibility of expanding DevPulse beyond GitHub to also support GitLab as a source provider.

## Event Compatibility

GitLab can produce equivalents for all DevPulse event types:

| DevPulse Event | GitHub Source | GitLab Equivalent | Notes |
|---|---|---|---|
| `pr` | Pull Request | Merge Request | Different name, similar data (additions/deletions/changed_files available) |
| `pr_review` | PR Review | MR Approval / MR Note | GitLab splits reviews into approvals + inline comments — no single "review" object |
| `issue` | Issue | Issue | Nearly identical concept |
| `issue_comment` | Issue Comment | Issue Note | GitLab calls comments "notes" |
| `fork` | Fork | Fork | Same concept, different API shape |
| Releases | GitHub Release | GitLab Release | Nearly identical |
| Container images | GitHub Packages | GitLab Container Registry | Different API, same concept |
| Repo metadata | Repository API | Project API | Stars = `star_count`, forks = `forks_count`, license = similar |

**Key difference:** GitLab's "Merge Request Review" is not a first-class object like GitHub's PR Review. It would need to be approximated from MR approvals + diff notes, which affects review latency and time-to-first-response metrics.

## Architecture: GitHub Coupling by Layer

The codebase breaks into three zones of GitHub specificity.

### Fully Generic (no changes needed)

These layers are already provider-agnostic and would work with GitLab data as-is:

- **Store interface & SQL queries** (`pkg/data/`, `pkg/data/postgres/`) — table and column names are generic (`org`, `repo`, `event`, `developer`)
- **Reputation scoring** (`pkg/data/postgres/reputation.go`) — works on generic developer/event records via `github.com/mchmarny/reputer`
- **LLM insights** (`pkg/data/insights_gen.go`) — Anthropic API call on generic `InsightsMetrics` structs
- **Dashboard charts & time-series queries** — all SQL operates on the generic `event` table
- **Data normalization** (`pkg/data/postgres/developer.go`, `entity.go`) — maps to generic `data.Developer` struct

### Lightly Coupled (1-2 weeks each)

- **OAuth flow** (`pkg/oauth/`) — standard OAuth2 with hard-coded GitHub URLs and scopes. GitLab uses the same OAuth2 flow with different endpoints (`https://gitlab.com/oauth/authorize`, scope `read_api`).
- **Webhooks** (`pkg/server/webhook.go`) — HMAC-SHA256 verification is standard; payload structure differs. Currently handles `installation` and `installation_repositories` events (GitHub App concepts).
- **Frontend** (`pkg/server/templates/`, `pkg/server/static/`) — text references to "GitHub", profile links to `github.com`, "Sign in with GitHub" branding. Functionally generic underneath.

### Deeply Coupled (bulk of the work)

- **Importer** (`pkg/importer/`, `pkg/data/postgres/event.go`) — 5 concurrent GitHub API importers (PRs, PR reviews, issues, issue comments, forks) plus release, container, and repo metadata importers. Each uses `go-github` v83 structs, GitHub-specific pagination (`ListOptions`), and GitHub field semantics.
- **GitHub App auth** (`pkg/tenant/githubapp.go`) — JWT creation + installation token minting. GitLab uses project/group access tokens instead of the App model.
- **ghutil helpers** (`pkg/data/ghutil/`) — user-to-developer mapping, rate limit handling (GitHub's 5000/hr + `X-RateLimit` headers), token pooling.
- **Rate limiting** — GitHub uses 5000 req/hr with 15-minute reset windows. GitLab uses 600 req/min. Pagination also differs (GitHub: `ListOptions`, GitLab: `per_page` + `page`).

### Files with `go-github` Imports

```
pkg/data/postgres/event.go
pkg/data/postgres/release.go
pkg/data/postgres/container.go
pkg/data/postgres/repo_meta.go
pkg/data/postgres/developer.go
pkg/data/postgres/reputation.go
pkg/data/ghutil/gh.go
pkg/tenant/githubapp.go
```

## Schema Considerations

The current database schema is largely provider-agnostic, but would need:

- A `provider` column on `tenant_repo` (and possibly `tenant`) to distinguish GitHub vs GitLab repos
- A provider-agnostic replacement for `github_app_installation` (or a parallel `gitlab_connection` table)
- No changes needed to `event`, `developer`, `repo_meta`, `release`, or other data tables — they already use generic field names

## Implementation Options

### Option 1: Provider Interface (recommended)

Introduce a `Provider` interface in `pkg/importer/`:

```go
type Provider interface {
    ImportPREvents(ctx context.Context, repo RepoRef, since time.Time) ([]data.Event, error)
    ImportIssueEvents(ctx context.Context, repo RepoRef, since time.Time) ([]data.Event, error)
    ImportReviews(ctx context.Context, repo RepoRef, since time.Time) ([]data.Event, error)
    ImportForks(ctx context.Context, repo RepoRef, since time.Time) ([]data.Event, error)
    ImportReleases(ctx context.Context, repo RepoRef) ([]data.Release, error)
    ImportRepoMeta(ctx context.Context, repo RepoRef) (*data.RepoMeta, error)
}
```

GitHub and GitLab each implement this interface, normalizing provider-specific API responses into the existing `data.Event` structs. The importer orchestrator stays unchanged.

**Pros:** Clean separation, testable, each provider is independent.
**Cons:** Largest upfront investment. Estimated effort: ~3 months.

### Option 2: Adapter Layer

Keep the existing importer structure but add a GitLab-to-GitHub translation layer that converts GitLab API responses into the same Go structs the GitHub importer expects. The importer code remains mostly unchanged.

**Pros:** Fastest path to working GitLab support.
**Cons:** Brittle — breaks when either API changes. Hides real differences (e.g., fragmented reviews). Estimated effort: ~6 weeks.

### Option 3: Separate Binary

Build `devpulse-import-gitlab` as a new binary that writes to the same database schema. Shares the Store layer but has its own import logic.

**Pros:** Zero risk to existing GitHub path. Can iterate independently.
**Cons:** Doubles maintenance surface. Shared logic (rate limiting, normalization) would need extraction. Estimated effort: ~2 months.

## Effort Breakdown (Option 1)

| Phase | Scope | Effort |
|---|---|---|
| Schema migration | Add provider column, abstract installations table | 1-2 weeks |
| Provider interface | Define interface, refactor GitHub importer to implement it | 2-3 weeks |
| OAuth abstraction | Provider-agnostic OAuth + token management | 1-2 weeks |
| GitLab importer | Concrete GitLab provider implementation | 2-3 weeks |
| Event mapping | MR approval + notes to review events, field normalization | 2-3 weeks |
| Webhook handler | Provider-aware webhook dispatcher | 1 week |
| Frontend updates | Provider-aware labels, links, sign-in flow | 1 week |
| Testing | Unit tests per provider, integration tests, E2E | 1-2 weeks |
| **Total** | | **11-17 weeks** |

## Risk Areas

- **Review metrics accuracy** — GitLab fragments reviews into approvals and notes. Time-to-first-response and review latency calculations will be approximations, not exact equivalents of the GitHub metrics.
- **Rate limiting differences** — GitLab's rate limiting model (per-minute vs per-hour) and response headers differ from GitHub. The token pooling strategy in `ghutil/tokenpool.go` would need a provider-specific implementation.
- **No App model in GitLab** — GitHub Apps provide scoped installation tokens per org. GitLab uses project/group access tokens or OAuth tokens. The tenant-to-provider auth relationship is fundamentally different.
- **Container registry divergence** — GitHub Packages and GitLab Container Registry have completely different APIs. Container version tracking would need per-provider implementations.
- **Self-hosted GitLab** — Unlike GitHub (mostly `github.com`), many GitLab instances are self-hosted with custom base URLs. The provider abstraction would need configurable API base URLs from the start.

## Summary

| Dimension | Assessment |
|---|---|
| Can GitLab produce equivalent events? | Yes, with one caveat (reviews are fragmented) |
| Codebase GitHub-specific | ~40% (importer + auth + ghutil + UI text) |
| Already generic | ~60% (data model, queries, charts, insights, reputation) |
| Biggest blocker | Importer — 900+ lines of GitHub API calls with provider-specific pagination, structs, and rate limiting |
| Recommended approach | Option 1: Provider interface, incremental rollout |
| Estimated effort | 3-4 months for full dual-provider support |
