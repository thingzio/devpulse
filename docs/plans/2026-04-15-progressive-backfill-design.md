# Progressive Backfill Design

## Problem

The current import pipeline fetches all events (up to 90 days) starting at page 1 on every run. For large repos (e.g. kubernetes/kubernetes with 43k+ events and 16k+ developers), a single run can't finish within the 55-minute Cloud Run job timeout. The event phase consumes all available time, starving subsequent phases (releases, metrics, reputation, insights). DB write duration degrades from ~1s to ~280s per batch as developer upsert conflict resolution grows with transaction size.

## Goals

1. **Fresh data fast** -- user sees 3 weeks of complete data (events + releases + reputation + insights) within the first import run, even for the largest repos
2. **Minimal DB impact** -- write transactions stay predictable in duration regardless of repo size or import progress
3. **Self-healing** -- killed jobs and API failures recover automatically on subsequent runs with no manual intervention

## Design

### Two-Pass Event Import

**Fresh pass** (always runs): fetches events from the most recent N days (default 21, configurable via `IMPORT_FRESH_DAYS`). Newest-first, all 5 event types concurrent via errgroup. This is the fast train -- always gets priority.

**Backfill pass** (best effort): after the fresh pass and all other phases complete, marches backward in fixed-size chunks (default 7 days, configurable via `IMPORT_BACKFILL_CHUNK_DAYS`) until reaching `EventAgeDaysDefault` (90 days). Each run advances one chunk.

### Revised Phase Ordering

```
importRepo(ctx, repo)
  1. Metadata
  2. Skip-unchanged check
  3. Fresh Pass -- events [today - IMPORT_FRESH_DAYS, today]
  4. Releases
  5. Metrics
  6. Containers
  7. Reputation
  8. Deep Reputation
  9. Insights
 10. Backfill Pass -- events [backfill_until - chunk, backfill_until]
 11. PR size backfill
```

Phases 4-9 run after the fresh pass so the dashboard has a complete dataset for the 3-week window before any time is spent on historical backfill.

### State Model

Add one column to `devpulse_state`:

```sql
ALTER TABLE devpulse_state ADD COLUMN backfill_until TIMESTAMP;
```

Semantics:
- `backfill_until` = oldest date covered so far. NULL means fresh repo (no backfill started).
- Fresh pass sets `backfill_until = today - IMPORT_FRESH_DAYS` on first run.
- Each backfill chunk advances it by `IMPORT_BACKFILL_CHUNK_DAYS`.
- Once it reaches `EventAgeDaysDefault` (90 days ago), backfill becomes a no-op.
- Updated only on successful chunk completion -- killed runs retry the same chunk.

Lifecycle for a new repo (defaults):

| Run | Fresh window | backfill_until after |
|-----|-------------|---------------------|
| 1 (on-demand) | today - 21d | today - 21d |
| 2 | today - 21d | today - 28d |
| 3 | today - 21d | today - 35d |
| ... | ... | ... |
| 10 | today - 21d | today - 90d (done) |
| 11+ | today - 21d | today - 90d (no-op) |

Full 90-day coverage in ~20 hours (10 runs at 2-hour intervals).

### DB Write Sub-Batching

API fetch batch size stays at 500 events. DB writes split into sub-batches:

- `IMPORT_DB_BATCH_SIZE` env var, default 100
- Each sub-batch is a separate transaction: BEGIN, sort by PK, upsert events, upsert developers, COMMIT
- State saved after all sub-batches for an API batch complete (not per sub-batch)
- Progress logging stays per API batch (every 500 events)

This keeps each transaction's developer conflict set small (~50-100 developers vs 16k+), maintaining flat ~1-3s write times throughout the import.

### Configuration

| Name | Type | Default | Description |
|------|------|---------|-------------|
| `IMPORT_DB_BATCH_SIZE` | env var | `100` | Events per DB transaction |
| `IMPORT_FRESH_DAYS` | env var | `21` | Fresh pass window (3 weeks) |
| `IMPORT_BACKFILL_CHUNK_DAYS` | env var | `7` | Backfill chunk size (1 week) |
| `EventAgeDaysDefault` | constant | `90` | Max backfill depth (unchanged) |

All env vars optional with sensible defaults. New behavior activates automatically.

### Self-Healing

**Job killed mid-fresh-pass:** Flushed sub-batches are persisted. State not advanced for in-progress API page. Next run re-fetches same window, upserts fill gaps.

**Job killed mid-backfill-chunk:** Fresh pass + all phases already completed. `backfill_until` not updated (written on chunk completion). Next run retries same chunk.

**GitHub API 500 on one event type:** `backfill_until` advances anyway -- the failing window ages out naturally. Fresh pass always covers recent data. Consistent with current retry behavior (1 retry, 2s backoff).

**Token pool exhausted:** Current rotation/exhaustion behavior unchanged. Flushed sub-batches safe. Tokens reset on next run (60-min sliding window).

**Repo removed during backfill:** Excluded from `fetchWorkList()`. Orphaned state rows are harmless.

### Observability

New structured log fields:

```
fresh pass:     window_start, window_end, events, duration_sec
backfill pass:  chunk_start, chunk_end, events, duration_sec, coverage_days, target_days
backfill done:  coverage_days (one-time INFO)
backfill skip:  (DEBUG, already at full coverage)
```

No new alerts -- existing `import-failure` and `import-repo-errors` cover both passes.

### What Doesn't Change

- Skip-unchanged optimization (pushed_at vs max_event_time)
- Sharding, token pool, worker concurrency
- On-demand import trigger (runs same new flow for single repo)
- All non-event phases (releases, metrics, reputation, insights)
- 500-event API fetch batch size
- Retry behavior (1 retry on 500, 2s backoff)
- Upsert semantics (ON CONFLICT DO UPDATE)

## Impact on Existing Users

This change is fully transparent to current users. No action required.

**No downtime:** `ALTER TABLE ADD COLUMN backfill_until TIMESTAMP` is non-blocking in PostgreSQL (nullable, no default). Applied automatically by the migration runner on next deploy.

**Existing repos with full coverage:** On first run with new code, `backfill_until` is NULL. The fresh pass imports the recent 3-week window (subset of already-imported data -- upserts are no-ops). After the fresh pass, the importer detects existing event coverage and sets `backfill_until` to the actual oldest event date. If already at 90 days, backfill becomes a no-op immediately.

**No UI changes:** Dashboard reads from the same tables with the same queries. Users see no difference for repos already at full coverage.

**No config changes required:** All new env vars have sensible defaults. Deploy the new binary and it works.

**Only observable difference:** Newly added large repos show data sooner (3 weeks immediately instead of waiting for a 90-day fetch that may timeout), with historical data filling in over ~24 hours.

## Rejected Alternatives

**Priority Queue with Event Budgets:** Per-repo event budget, stop when exhausted. Requires page-level state tracking, budget tuning is fragile, doesn't cleanly separate fresh from backfill.

**Time-Boxed Phases:** Each phase gets a time budget. Hard to tune across repo sizes, small repos waste allocated time, no explicit backfill progress visibility.
