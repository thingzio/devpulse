# Performance

Method, baselines, and findings for query tuning against the shared
`thingzio-pg` instance. Numbers are dated — re-measure before trusting them.

## The binding constraint

`thingzio-pg` is **`db-custom-1-3840`**: 1 vCPU, 3.75 GB RAM, PD_SSD, running
PostgreSQL 16. It is shared by devpulse, devtrace, and devradar.

That single fact drives everything below. With under 4 GB of RAM split three
ways, **buffer traffic is the constraint, not CPU**. A query that touches
gigabytes of buffers cannot stay resident, so it goes disk-bound — and it
evicts other queries' pages on the way, degrading endpoints that were never
slow to begin with.

**Wall-clock on a laptop will not show you this.** A dev machine has enough
page cache to hold the entire working set, so a pathological query and a good
one both look fine. Measure `Shared Hit Blocks + Shared Read Blocks`.

## Method

Reproduce production locally rather than reasoning about plans:

```bash
# 1. Postgres matching production's major version
docker run -d --name pgperf -p 55432:5432 \
  -e POSTGRES_PASSWORD=x -e POSTGRES_DB=thingz postgres:16

# 2. Restore the most recent dump (see docs below for location)
gzcat ~/dev/thingz/db/thingz-YYYYMMDD-HHMMSS.sql.gz \
  | docker exec -i pgperf psql -U postgres -d thingz -q
# "role does not exist" errors are GRANT statements; harmless for analysis

# 3. Bring the schema to current (dumps predate recent migrations)
for f in pkg/data/postgres/sql/migrations/0*.sql; do
  docker cp "$f" pgperf:/tmp/ && docker exec pgperf psql -U postgres -d thingz -f "/tmp/$(basename $f)"
done
docker exec pgperf psql -U postgres -d thingz -c "VACUUM ANALYZE;"
```

Then measure buffers, not time:

```sql
EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) <query>;
-- read Plan."Shared Hit Blocks" + Plan."Shared Read Blocks" from the root node
-- blocks x 8 KB = bytes of buffer traffic
```

Use a non-default port (`55432`); `5432` is usually taken by another project's
`docker-compose` Postgres.

Note port `5432` conflicts and the local `docker-compose.yaml` hardcode it, so
`make e2e` and a restored dump cannot both run at once.

**Verify any rewrite against real data**, not just tests: run the old and new
query side by side and `diff` the output. Row counts and aggregate semantics
are easy to get subtly wrong, and prod data has shapes fixtures do not.

## Findings

### Correlated EXISTS inside COUNT(DISTINCT CASE ...) — 2026-08-15

`tenantRepoOverviewSQL` used a per-repo `LATERAL` containing:

```sql
COUNT(DISTINCT CASE WHEN <filter>
  AND EXISTS (SELECT 1 FROM devpulse_developer d
              WHERE d.username = e.username AND d.reputation IS NOT NULL)
  THEN e.username END)
```

The planner **cannot** flatten an `EXISTS` nested inside an aggregate's `CASE`
into a semi-join. For a 25-repo tenant it executed **94,022 times**, consuming
378,068 buffer blocks (~2.9 GB) to return 25 rows — 99.98% of the query's I/O.

Rewriting it as a single grouped pass with one join to `devpulse_developer`:
**3,516 blocks (~27 MB), a 108x reduction**, byte-identical output.

**Counter-intuitive result worth remembering:** an intermediate attempt that
kept the `LATERAL` and only swapped the `EXISTS` for a join measured *worse*
than the original (454,241 blocks) — the developer join gets rebuilt once per
repo. Restructuring beat patching. This is why variants get measured rather
than argued about.

### TEXT vs native temporal types — 2026-08-15

Migrating 12 columns from TEXT to `DATE`/`TIMESTAMPTZ` (262K rows), measured
best-of-7 on identical index sets:

| Query shape | TEXT | native | Δ |
|---|---|---|---|
| Range scan `org+repo+date >=` | 0.07 ms | 0.06 ms | — |
| Full-table date range | 1.85 ms | 1.86 ms | — |
| `MAX(created_at)` for org/repo | 0.05 ms | 0.05 ms | — |
| Sort `created_at DESC LIMIT 50` | 0.04 ms | 0.04 ms | — |
| Monthly rollup via `SUBSTRING` | 22.3 ms | 21.8 ms | — |
| `GROUP BY date` | 19.3 ms | 12.5 ms | **−35%** |
| `created_at::date >=` | 14.8 ms | 11.3 ms | **−24%** |
| Bulk insert 50k | 570 ms | 432 ms | **−24%** |
| Storage (10 indexes) | 116 MB | 89 MB | **−23%** |

**ISO-8601 text sorts identically to chronological order**, so btree range
scans were never the problem — a common assumption that does not hold here.
Gains are confined to aggregation, write throughput, and disk. Do not expect
type changes alone to fix a slow endpoint.

`ALTER TABLE` rewriting `devpulse_event` (262K rows, 10 indexes) took ~2.8s
locally and ~14.6s on Cloud SQL. Migrations run during container startup, so
the Cloud Run startup probe budget must exceed the slowest migration.

## Endpoint baselines

Uncached, warm container. `/api/repos/overview` is served by an in-process
cache (`apiCache`), so repeat requests return in ~5 ms and **do not measure the
query** — only the first request after an invalidation is a real sample.

| Endpoint | Before | After temporal types | After query rewrite |
|---|---|---|---|
| `/api/repos/overview` | 20.8–24.9s | 14.7–16.2s | **9.62s** |
| `/data/insights/summary` (180d, all repos) | 6.4s | 5.1–5.6s | **2.57s** |

`/data/insights/summary` improved without being modified — consistent with the
overview query no longer evicting its pages from shared buffers. Reducing one
query's buffer churn measurably helps unrelated queries on this instance.

## Open

`/api/repos/overview` remains **9.6s in production versus 240 ms locally on the
same data and plan** — roughly 40x unexplained. The query plan is no longer the
bottleneck; something around it is. Untested candidates: connection acquisition
through the Cloud SQL socket, pool warm-up on the first request after idle, or
vCPU contention with devtrace/devradar.

Next step is measurement, not more tuning: instrument `GetOverview` to log the
DB call duration separately from total request latency, which separates
Postgres time from everything else in a single dashboard load.

The `9.62s` figure is a **single sample**. Treat it as directional.

## Related

- [`INFRASTRUCTURE.md`](INFRASTRUCTURE.md) — instance sizing and cost
- [`.claude/CLAUDE.md`](../.claude/CLAUDE.md) — migration numbering rules; a
  mis-numbered migration is skipped silently
