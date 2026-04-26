package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thingzio/devpulse/pkg/data"
)

func TestParseRepo(t *testing.T) {
	tests := []struct {
		name     string
		input    *string
		wantOrg  string
		wantRepo string
		wantOK   bool
	}{
		{name: "nil input", input: nil, wantOK: false},
		{name: "valid org/repo", input: strPtr("myorg/myrepo"), wantOrg: "myorg", wantRepo: "myrepo", wantOK: true},
		{name: "no slash", input: strPtr("justarepo"), wantOK: false},
		{name: "whitespace trimmed", input: strPtr("  myorg  /  myrepo  "), wantOrg: "myorg", wantRepo: "myrepo", wantOK: true},
		{name: "empty string", input: strPtr(""), wantOK: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			org, repo, ok := parseRepo(tc.input)
			assert.Equal(t, tc.wantOK, ok)
			if tc.wantOK {
				require.NotNil(t, org)
				require.NotNil(t, repo)
				assert.Equal(t, tc.wantOrg, *org)
				assert.Equal(t, tc.wantRepo, *repo)
			}
		})
	}
}

func TestQueryParamInt(t *testing.T) {
	tests := []struct {
		name  string
		query string
		key   string
		def   int
		want  int
	}{
		{name: "missing key uses default", query: "", key: "d", def: 180, want: 180},
		{name: "valid value", query: "d=90", key: "d", def: 180, want: 90},
		{name: "below range uses default", query: "d=0", key: "d", def: 180, want: 180},
		{name: "above range uses default", query: "d=3651", key: "d", def: 180, want: 180},
		{name: "boundary low valid", query: "d=14", key: "d", def: 180, want: 14},
		{name: "boundary high valid", query: "d=3650", key: "d", def: 180, want: 3650},
		{name: "non-numeric uses default", query: "d=abc", key: "d", def: 180, want: 180},
		{name: "empty value uses default", query: "d=", key: "d", def: 180, want: 180},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/data?"+tc.query, nil)
			got := queryParamInt(r, tc.key, tc.def)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestMapCountedItemsToSeries(t *testing.T) {
	t.Run("empty input", func(t *testing.T) {
		res := mapCountedItemsToSeries(nil)
		assert.Equal(t, []string{"ALL OTHERS"}, res.Labels)
		assert.Equal(t, []int{100}, res.Data)
	})

	t.Run("exactly 9 items summing to 100 no OTHER", func(t *testing.T) {
		items := make([]*data.CountedItem, 9)
		for i := range items {
			items[i] = &data.CountedItem{Name: string(rune('a' + i)), Count: 11}
		}
		// 9 * 11 = 99, still < 100, so OTHER is added
		res := mapCountedItemsToSeries(items)
		assert.Len(t, res.Labels, 10)
		assert.Equal(t, "ALL OTHERS", res.Labels[9])
		assert.Equal(t, 1, res.Data[9]) // 100 - 99 = 1
	})

	t.Run("sum equals 100 no OTHER row", func(t *testing.T) {
		items := []*data.CountedItem{
			{Name: "a", Count: 60},
			{Name: "b", Count: 40},
		}
		res := mapCountedItemsToSeries(items)
		assert.Len(t, res.Labels, 2)
		assert.NotContains(t, res.Labels, "ALL OTHERS")
	})

	t.Run("more than 9 items truncated", func(t *testing.T) {
		items := make([]*data.CountedItem, 15)
		for i := range items {
			items[i] = &data.CountedItem{Name: string(rune('a' + i)), Count: 5}
		}
		res := mapCountedItemsToSeries(items)
		// truncated to 9, sum=45, OTHER=55
		assert.Equal(t, 10, len(res.Labels))
		assert.Equal(t, "ALL OTHERS", res.Labels[9])
		assert.Equal(t, 55, res.Data[9])
	})
}

func TestOptional(t *testing.T) {
	assert.Nil(t, optional(""))
	p := optional("value")
	require.NotNil(t, p)
	assert.Equal(t, "value", *p)
}

func TestParseInsightParams(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/data", nil)
		p := parseInsightParams(r)
		assert.Equal(t, data.EventAgeDaysDefault, p.days)
		assert.Nil(t, p.org)
		assert.Nil(t, p.repo)
	})

	t.Run("explicit days", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/data?d=90", nil)
		p := parseInsightParams(r)
		assert.Equal(t, 90, p.days)
	})

	t.Run("org/repo from r param", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/data?r=myorg/myrepo", nil)
		p := parseInsightParams(r)
		require.NotNil(t, p.org)
		require.NotNil(t, p.repo)
		assert.Equal(t, "myorg", *p.org)
		assert.Equal(t, "myrepo", *p.repo)
	})
}

func TestEntityDevelopersAPIHandler_MissingEntity(t *testing.T) {
	h := entityDevelopersAPIHandler(nil)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/data/entity-devs", nil)
	h(w, r)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "entity parameter required")
}

func TestDeveloperSearchAPIHandler_MissingQuery(t *testing.T) {
	h := developerSearchAPIHandler(nil)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/data/devs/search", nil)
	h(w, r)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "query parameter (q) is required")
}

func TestContributorProfileAPIHandler_MissingUsername(t *testing.T) {
	h := insightsContributorProfileAPIHandler(nil)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/data/contributor", nil)
	h(w, r)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "username parameter (u) is required")
}

func TestEventSearchAPIHandler_InvalidJSON(t *testing.T) {
	h := eventSearchAPIHandler(nil)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/data/events/search", strings.NewReader("not json"))
	h(w, r)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestQueryAPIHandler_DefaultScope(t *testing.T) {
	h := queryAPIHandler(nil)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/data/query?v=unknown", nil)
	h(w, r)
	assert.Equal(t, http.StatusOK, w.Code)
}

func strPtr(s string) *string { return &s }

func TestResponseCache_GetSet(t *testing.T) {
	c := &responseCache{}

	// Miss on empty cache.
	_, ok := c.get("k1")
	assert.False(t, ok)

	// Hit after set.
	c.set("k1", []byte(`{"a":1}`))
	got, ok := c.get("k1")
	require.True(t, ok)
	assert.Equal(t, `{"a":1}`, string(got))

	// Different key is still a miss.
	_, ok = c.get("k2")
	assert.False(t, ok)
}

func TestResponseCache_TTLExpiry(t *testing.T) {
	c := &responseCache{}

	// Insert with an already-expired entry.
	c.setWithTTL("old", []byte(`{}`), -1*time.Second)

	_, ok := c.get("old")
	assert.False(t, ok, "expired entry should not be returned")
}

func TestResponseCache_BoundedSize(t *testing.T) {
	c := &responseCache{maxEntries: 10}

	// Insert more than the cap.
	for i := 0; i < 25; i++ {
		c.set(strconv.Itoa(i), []byte("x"))
	}

	// Cache must not exceed cap (excess is target = limit - 10%).
	assert.LessOrEqual(t, c.len(), 10, "cache should not exceed max")
	assert.Greater(t, c.len(), 0, "eviction should not empty the cache")
}

func TestResponseCache_InvalidatePrefix(t *testing.T) {
	c := &responseCache{}
	c.set("tenant-a|/data/foo", []byte("1"))
	c.set("tenant-a|/data/bar", []byte("2"))
	c.set("tenant-b|/data/foo", []byte("3"))

	c.invalidatePrefix("tenant-a|")

	_, ok := c.get("tenant-a|/data/foo")
	assert.False(t, ok)
	_, ok = c.get("tenant-a|/data/bar")
	assert.False(t, ok)
	_, ok = c.get("tenant-b|/data/foo")
	assert.True(t, ok, "other tenant must be unaffected")
}

func TestDataCacheKey(t *testing.T) {
	r := httptest.NewRequest("GET", "/data/insights/summary?m=3&o=org1", nil)
	key := dataCacheKey(r)
	// No tenant in context, so key starts with "|".
	assert.Equal(t, "|/data/insights/summary?m=3&o=org1", key)
}

func TestInsightHandler_CacheHit(t *testing.T) {
	callCount := 0
	h := insightHandler(nil, "test", func(_ context.Context, _ data.Store, _, _ *string, _ int) (any, error) {
		callCount++
		return map[string]int{"count": 42}, nil
	})

	// First request — cache miss, calls the store function.
	r1 := httptest.NewRequest("GET", "/data/test?m=3", nil)
	w1 := httptest.NewRecorder()
	h.ServeHTTP(w1, r1)
	assert.Equal(t, http.StatusOK, w1.Code)
	assert.Equal(t, 1, callCount)
	assert.Equal(t, browserCacheMaxAge, w1.Header().Get(cacheControlHeaderKey))

	// Second request — cache hit, store function NOT called again.
	r2 := httptest.NewRequest("GET", "/data/test?m=3", nil)
	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, r2)
	assert.Equal(t, http.StatusOK, w2.Code)
	assert.Equal(t, 1, callCount, "store function should not be called on cache hit")
	assert.Equal(t, w1.Body.String(), w2.Body.String(), "cached response should match")
}
