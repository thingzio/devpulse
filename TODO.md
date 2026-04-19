# TODO

Pending work items, future improvements, and technical debt.

## Plan Features (Phase 2)

Documented in [docs/PLANS.md](docs/PLANS.md). Remaining items:

- [ ] **API Access (Enterprise)** — PAT authentication, in-memory token cache, per-tenant rate limiting
- [ ] **Private Repos (Enterprise)** — Remove public repo check for Enterprise, add to plan limits
- [ ] **Import Frequency** — Per-plan scheduled import intervals (daily/hourly)

## Platform

- [ ] **GitLab support** — Feasibility assessment in [docs/GITLAB.md](docs/GITLAB.md). 3-4 month effort for full dual-provider support. Key challenges: MR approval model, rate limiting model, no GitHub Apps equivalent.

## Operational

- [ ] **Concurrent job execution guard** — Cloud Scheduler may launch a new execution while a manually-triggered one is still running. Consider adding a check in `runImport` (e.g., advisory lock or execution-count check) to skip if another execution is active.
- [ ] **Stripe billing integration** — Remote branch `feat/stripe-integration` exists, needs rebase.
