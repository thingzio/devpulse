package server

import (
	"context"
	"log/slog"
	"net/http"
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
	browserCacheMaxAge    = "private, max-age=1800"
	cacheControlHeaderKey = "Cache-Control"
)

// responseCache is a TTL-based in-memory cache for JSON API responses.
type responseCache struct {
	entries sync.Map
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
				now := time.Now()
				c.entries.Range(func(key, value any) bool {
					if e, ok := value.(*cacheEntry); ok && now.After(e.expires) {
						c.entries.Delete(key)
					}
					return true
				})
			}
		}
	}()
}

func (c *responseCache) get(key string) ([]byte, bool) {
	v, ok := c.entries.Load(key)
	if !ok {
		return nil, false
	}
	e := v.(*cacheEntry)
	if time.Now().After(e.expires) {
		c.entries.Delete(key)
		return nil, false
	}
	return e.data, true
}

func (c *responseCache) set(key string, val []byte) {
	c.entries.Store(key, &cacheEntry{
		data:    val,
		expires: time.Now().Add(serverCacheTTL),
	})
}

// dataCacheKey builds a cache key from tenant ID, path, and query string.
// Strips jQuery's cache-buster parameter (_=timestamp) so repeat requests
// from $.ajaxSetup({cache:false}) still hit the server-side cache.
func dataCacheKey(r *http.Request) string {
	tid := ""
	if tn := middleware.TenantFromContext(r.Context()); tn != nil {
		tid = tn.ID
	}
	q := r.URL.Query()
	q.Del("_")
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
