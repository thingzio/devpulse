# TODO

Pending work items, future improvements, and technical debt.

## Import Pipeline

- [ ] **Legacy claim queue columns** — Drop `import_claimed_at`, `import_claimed_by`, `import_done_at` columns and `idx_tenant_repo_import_queue` index from `tenant_repo` table. Retained for rollback safety after sharded import refactor (v0.35.0).

## Plan Features (Phase 2)

Documented in `docs/PLANS.md`. Recommended implementation order:

- [ ] **API Access (Enterprise)** — PAT authentication via `Authorization: Bearer` header, in-memory token cache, per-tenant rate limiting.
- [ ] **Private Repos (Enterprise)** — Remove public repo check for Enterprise, add `PrivateRepos` to plan limits.
- [ ] **Import Frequency** — Per-plan import intervals (daily/hourly), `ImportIntervalHours` in plan limits.

## Platform

- [ ] **GitLab support** — Feasibility assessment in `docs/GITLAB.md`. 3-4 month effort for full dual-provider support. Key challenges: MR approval model differences, rate limiting model, no GitHub Apps equivalent.

## Operational

- [ ] **Concurrent job execution guard** — Cloud Scheduler may launch a new execution while a manually-triggered one is still running. Consider adding a check in `runImport` (e.g., advisory lock or execution-count check) to skip if another execution is active.
- [ ] **Stripe billing integration** — `feat/stripe-integration` branch exists, needs rebase. Docs in `docs/STRIPE.md`.
- [ ] **Token quota time-series** — Sample GitHub API token utilization (used/limit per installation) a few times per hour during import, write as GCP custom metric. Would surface quota exhaustion mid-import that point-in-time checks on `/admin/tokens` miss (quota resets on a 60-min sliding window).
