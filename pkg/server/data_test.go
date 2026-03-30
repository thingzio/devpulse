package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
		{name: "missing key uses default", query: "", key: "m", def: 6, want: 6},
		{name: "valid value", query: "m=12", key: "m", def: 6, want: 12},
		{name: "below range uses default", query: "m=0", key: "m", def: 6, want: 6},
		{name: "above range uses default", query: "m=121", key: "m", def: 6, want: 6},
		{name: "boundary low valid", query: "m=1", key: "m", def: 6, want: 1},
		{name: "boundary high valid", query: "m=120", key: "m", def: 6, want: 120},
		{name: "non-numeric uses default", query: "m=abc", key: "m", def: 6, want: 6},
		{name: "empty value uses default", query: "m=", key: "m", def: 6, want: 6},
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
		assert.Equal(t, data.EventAgeMonthsDefault, p.months)
		assert.Nil(t, p.org)
		assert.Nil(t, p.repo)
	})

	t.Run("explicit months", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/data?m=3", nil)
		p := parseInsightParams(r)
		assert.Equal(t, 3, p.months)
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
