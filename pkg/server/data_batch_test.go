package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thingzio/devpulse/pkg/plan"
)

func TestBatchHealthHandler(t *testing.T) {
	clearAPICache()
	ms := &mockStore{}
	h := batchHealthHandler(ms)

	r := httptest.NewRequest(http.MethodGet, "/data/batch/health?d=28&o=test&r=test/repo", nil)
	r = withTenant(r, testTenant(plan.Pro))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))

	var resp batchHealthResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
}

func TestBatchActivityHandler(t *testing.T) {
	clearAPICache()
	ms := &mockStore{}
	h := batchActivityHandler(ms)

	r := httptest.NewRequest(http.MethodGet, "/data/batch/activity?d=28&o=test&r=test/repo", nil)
	r = withTenant(r, testTenant(plan.Pro))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp batchActivityResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
}

func TestBatchVelocityHandler(t *testing.T) {
	clearAPICache()
	ms := &mockStore{}
	h := batchVelocityHandler(ms)

	r := httptest.NewRequest(http.MethodGet, "/data/batch/velocity?d=28&o=test&r=test/repo", nil)
	r = withTenant(r, testTenant(plan.Pro))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp batchVelocityResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
}

func TestBatchQualityHandler(t *testing.T) {
	clearAPICache()
	ms := &mockStore{}
	h := batchQualityHandler(ms)

	r := httptest.NewRequest(http.MethodGet, "/data/batch/quality?d=28&o=test&r=test/repo", nil)
	r = withTenant(r, testTenant(plan.Pro))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp batchQualityResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
}

func TestBatchCommunityHandler(t *testing.T) {
	clearAPICache()
	ms := &mockStore{}
	h := batchCommunityHandler(ms)

	r := httptest.NewRequest(http.MethodGet, "/data/batch/community?d=28&o=test&r=test/repo", nil)
	r = withTenant(r, testTenant(plan.Pro))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp batchCommunityResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
}

func TestBatchHandler_CacheHit(t *testing.T) {
	clearAPICache()
	ms := &mockStore{}
	h := batchHealthHandler(ms)
	tn := testTenant(plan.Pro)

	// First call: cache miss.
	r1 := httptest.NewRequest(http.MethodGet, "/data/batch/health?d=28&o=test&r=test/repo", nil)
	r1 = withTenant(r1, tn)
	w1 := httptest.NewRecorder()
	h.ServeHTTP(w1, r1)
	require.Equal(t, http.StatusOK, w1.Code)

	// Second call: cache hit.
	r2 := httptest.NewRequest(http.MethodGet, "/data/batch/health?d=28&o=test&r=test/repo", nil)
	r2 = withTenant(r2, tn)
	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, r2)
	require.Equal(t, http.StatusOK, w2.Code)

	assert.Equal(t, w1.Body.String(), w2.Body.String(), "cached response must match")
}
