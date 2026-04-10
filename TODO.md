# TODO

Pending work items, future improvements, and technical debt.

## Import Pipeline

- [ ] **Weighted task sharding** — Shard repos by last import event count (descending round-robin) instead of alphabetical. Balances task completion times. New repos without history use median weight. Change isolated to `ShardRepos` in `pkg/importer/shard.go`.
- [ ] **Task-level scope logging** — After shard assignment, log total new events since last import, stale contributors needing deep rep, and backfill candidates per task. Enables duration projection and progress percentage in `tools/job-detail`.
- [ ] **Legacy claim queue columns** — Drop `import_claimed_at`, `import_claimed_by`, `import_done_at` columns and `idx_tenant_repo_import_queue` index from `tenant_repo` table. Retained for rollback safety after sharded import refactor (v0.35.0).
- [ ] **Duplicate `importing repo` log lines** — Each repo logs twice: once from `importRepoWork` (with plan/tenants) and once from `importRepo` (without). Consolidate to single log line.
- [ ] **Admin metrics review deeprep references** — `pkg/admin/metrics.go` still queries deeprep-specific log-based metrics (scored, rate-limit-pauses, errors) that no longer exist. Update to query import job deep rep logs instead.

## Plan Features (Phase 2)

Documented in `docs/PLANS.md`. Recommended implementation order:

- [ ] **API Access (Enterprise)** — PAT authentication via `Authorization: Bearer` header, in-memory token cache, per-tenant rate limiting.
- [ ] **Private Repos (Enterprise)** — Remove public repo check for Enterprise, add `PrivateRepos` to plan limits.
- [ ] **Import Frequency** — Per-plan import intervals (daily/hourly), `ImportIntervalHours` in plan limits.
- [ ] **On-demand Import (Enterprise)** — Trigger immediate repo import via Cloud Run Jobs API from site binary.

## Platform

- [ ] **GitLab support** — Feasibility assessment in `docs/GITLAB.md`. 3-4 month effort for full dual-provider support. Key challenges: MR approval model differences, rate limiting model, no GitHub Apps equivalent.

## Operational

- [ ] **Concurrent job execution guard** — Cloud Scheduler may launch a new execution while a manually-triggered one is still running. Consider adding a check in `runImport` (e.g., advisory lock or execution-count check) to skip if another execution is active.
- [ ] **Stripe billing integration** — `feat/stripe-integration` branch exists, needs rebase. Docs in `docs/STRIPE.md`.
