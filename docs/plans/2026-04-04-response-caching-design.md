# Response Caching Design

**Date:** 2026-04-04
**Status:** Approved

## Problem

Dashboard chart API endpoints hit PostgreSQL on every request. With db-g1-small (shared vCPU), concurrent aggregation queries from multiple users cause p99 latency > 500ms. Data only changes at hourly import, so most queries return identical results.

## Solution

Two-layer caching:

1. **Server-side in-memory cache** -- `sync.Map` keyed by `(tenant_id, path, query)`, 5-minute TTL. Protects DB from concurrent requests across all users.
2. **Browser cache** -- `Cache-Control: private, max-age=1800` on all `/data/` responses. Prevents repeat fetches on tab switches and page reloads.

## Cache Key

```
tenant_id + request.URL.Path + request.URL.RawQuery
```

Tenant ID from `middleware.TenantFromContext()`. Path + query covers endpoint, org, repo, entity, months.

## Invalidation

None. TTL-only expiry. No complexity.

## Memory Estimate

- ~1KB per cached response, ~50 entries per active tenant
- 80 tenants: ~4MB. 1,000 tenants: ~50MB. Negligible against 512MB container.

## Code Changes

### `pkg/server/data.go`

- Add `responseCache` struct wrapping `sync.Map` with TTL-based expiry
- Add `cacheEntry` struct holding response bytes and expiry time
- Add `get(key)` and `set(key, value)` methods on `responseCache`
- Modify `insightHandler` to check cache before calling Store, cache JSON bytes after
- Modify `insightWithEntityHandler` same pattern
- Add `Cache-Control: private, max-age=1800` header in both factories
- Standalone handlers that don't use factories get `Cache-Control` header only

### Excluded from caching

- `/data/export/csv` -- export endpoint, not cacheable
- `/data/search` (POST) -- search endpoint, not cacheable
- `/data/developer/search` -- autocomplete, fast and unique per keystroke

## What's NOT Changing

- No new dependencies (stdlib only: sync, time)
- No config/env vars
- Frontend JS unchanged
- RLS scoping unchanged -- cache is keyed per tenant
- No invalidation logic
