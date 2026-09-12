// Copyright 2026 Thingz LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"context"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/thingzio/devpulse/pkg/config"
	"github.com/thingzio/devpulse/pkg/data"
	"github.com/thingzio/devpulse/pkg/middleware"
	"github.com/thingzio/devpulse/pkg/plan"
)

const (
	serverCacheTTL        = 5 * time.Minute
	browserCacheMaxAge    = "private, max-age=600"
	cacheControlHeaderKey = "Cache-Control"
)

// defaultCacheMaxEntries caps the number of cache entries to bound memory.
// At ~100KB per JSON response this allows up to roughly 500MB of cached data.
// When the cap is exceeded we evict the entries with the soonest expiry.
const defaultCacheMaxEntries = 5000

// responseCache is a bounded TTL-based in-memory cache for JSON API responses.
// The map+mutex layout lets the cache enforce a hard size cap so a misbehaving
// caller (or an unbounded path/query namespace) cannot exhaust process memory.
type responseCache struct {
	mu         sync.RWMutex
	entries    map[string]*cacheEntry
	maxEntries int
}

type cacheEntry struct {
	data    []byte
	expires time.Time
}

var apiCache = &responseCache{}

// startEviction runs a background goroutine that periodically removes expired
// cache entries. It stops when ctx is canceled.
func (c *responseCache) startEviction(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				c.purgeExpired()
			}
		}
	}()
}

func (c *responseCache) purgeExpired() {
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	for k, e := range c.entries {
		if now.After(e.expires) {
			delete(c.entries, k)
		}
	}
}

func (c *responseCache) get(key string) ([]byte, bool) {
	c.mu.RLock()
	e, ok := c.entries[key]
	c.mu.RUnlock()
	if !ok {
		return nil, false
	}
	if time.Now().After(e.expires) {
		c.mu.Lock()
		// Re-check under write lock — another goroutine may have refreshed.
		if cur, still := c.entries[key]; still && cur == e {
			delete(c.entries, key)
		}
		c.mu.Unlock()
		return nil, false
	}
	return e.data, true
}

func (c *responseCache) set(key string, val []byte) {
	c.setWithTTL(key, val, serverCacheTTL)
}

func (c *responseCache) setWithTTL(key string, val []byte, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = make(map[string]*cacheEntry)
	}
	c.entries[key] = &cacheEntry{
		data:    val,
		expires: time.Now().Add(ttl),
	}
	c.evictLocked()
}

// evictLocked enforces maxEntries by removing the soonest-to-expire entries.
// Caller must hold c.mu. Costs O(n) but only runs when the cap is hit.
func (c *responseCache) evictLocked() {
	limit := c.maxEntries
	if limit <= 0 {
		limit = defaultCacheMaxEntries
	}
	if len(c.entries) <= limit {
		return
	}

	// First pass: drop already-expired entries — that may be enough.
	now := time.Now()
	for k, e := range c.entries {
		if now.After(e.expires) {
			delete(c.entries, k)
		}
	}
	if len(c.entries) <= limit {
		return
	}

	// Still over: evict the entries with the smallest expires time until
	// we drop ~10% under the cap to amortize future evictions.
	target := limit - limit/10
	excess := len(c.entries) - target
	type kv struct {
		key     string
		expires time.Time
	}
	victims := make([]kv, 0, len(c.entries))
	for k, e := range c.entries {
		victims = append(victims, kv{k, e.expires})
	}
	sort.Slice(victims, func(i, j int) bool {
		return victims[i].expires.Before(victims[j].expires)
	})
	for i := 0; i < excess && i < len(victims); i++ {
		delete(c.entries, victims[i].key)
	}
}

func (c *responseCache) invalidatePrefix(prefix string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for k := range c.entries {
		if strings.HasPrefix(k, prefix) {
			delete(c.entries, k)
		}
	}
}

func (c *responseCache) len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.entries)
}

const maxCacheTTL = 30 * time.Minute

// requestCacheTTL returns the cache TTL from the ttl query param (seconds),
// clamped between serverCacheTTL and maxCacheTTL. Falls back to serverCacheTTL.
func requestCacheTTL(r *http.Request) time.Duration {
	v := r.URL.Query().Get("ttl")
	if v == "" {
		return serverCacheTTL
	}
	secs, err := strconv.Atoi(v)
	if err != nil || secs <= 0 {
		return serverCacheTTL
	}
	ttl := time.Duration(secs) * time.Second
	if ttl < serverCacheTTL {
		return serverCacheTTL
	}
	if ttl > maxCacheTTL {
		return maxCacheTTL
	}
	return ttl
}

// dataCacheKey builds a cache key from tenant ID, path, and query string.
// Strips jQuery's cache-buster (_=timestamp) and ttl param so repeat requests
// with different cache hints still hit the same cache entry.
func dataCacheKey(r *http.Request) string {
	tid := ""
	if tn := middleware.TenantFromContext(r.Context()); tn != nil {
		tid = tn.ID
	}
	q := r.URL.Query()
	q.Del("_")
	q.Del("ttl")
	return tid + "|" + r.URL.Path + "?" + q.Encode()
}

const (
	percentageListLimit           = 9
	repoNamePartsLimit            = 2
	hundredPercent                = 100
	categoryOther                 = "ALL OTHERS"
	arraySelector                 = "|"
	maxRequestBodyBytes     int64 = 1 << 20 // 1 MB
	queryResultLimitDefault       = 500
)

// SeriesData is a generic type for chart series responses.
type SeriesData[T any] struct {
	Labels []string `json:"labels"`
	Data   []T      `json:"data"`
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func queryParamInt(r *http.Request, key string, def int) int {
	v := r.URL.Query().Get(key)
	if v == "" {
		return def
	}

	i, err := strconv.Atoi(v)
	if err != nil {
		slog.Warn("error converting query string to int", "value", v, "error", err)
		return def
	}

	if i < config.QueryParamMinDays() || i > config.QueryParamMaxDays() {
		return def
	}

	return i
}

func optional(val string) *string {
	if val == "" {
		return nil
	}
	return &val
}

func parseRepo(repo *string) (*string, *string, bool) {
	if repo == nil {
		return nil, nil, false
	}

	repoParts := strings.Split(*repo, "/")
	if len(repoParts) != repoNamePartsLimit {
		return nil, nil, false
	}

	o := strings.TrimSpace(repoParts[0])
	r := strings.TrimSpace(repoParts[1])

	return &o, &r, true
}

type insightParams struct {
	days int
	org  *string
	repo *string
}

func parseInsightParams(r *http.Request) insightParams {
	days := queryParamInt(r, "d", data.EventAgeDaysDefault)
	if tn := middleware.TenantFromContext(r.Context()); tn != nil {
		if limits, ok := plan.Get(tn.Plan); ok && limits.MaxDataRangeMonths > 0 {
			maxDays := limits.MaxDataRangeMonths * 30
			if days > maxDays {
				days = maxDays
			}
		}
	}
	org := r.URL.Query().Get("o")
	repo := r.URL.Query().Get("r")
	if orgStr, repoStr, ok := parseRepo(optional(repo)); ok {
		org = *orgStr
		repo = *repoStr
	}
	return insightParams{days: days, org: optional(org), repo: optional(repo)}
}

func mapCountedItemsToSeries(res []*data.CountedItem) *SeriesData[int] {
	slog.Debug("items", "count", len(res))

	if len(res) > percentageListLimit {
		res = res[:percentageListLimit]
	}

	sum := 0
	d := &SeriesData[int]{
		Labels: make([]string, 0),
		Data:   make([]int, 0),
	}
	for _, v := range res {
		sum += v.Count
		d.Labels = append(d.Labels, v.Name)
		d.Data = append(d.Data, v.Count)
	}

	if sum < hundredPercent {
		d.Labels = append(d.Labels, categoryOther)
		d.Data = append(d.Data, hundredPercent-sum)
	}
	return d
}

// storeFromRequest returns the tenant-scoped Store from context, falling back to the default.
func storeFromRequest(r *http.Request, fallback data.Store) data.Store {
	if s := scopedStoreFromContext(r.Context()); s != nil {
		return s
	}
	return fallback
}
