package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteJSON(t *testing.T) {
	t.Parallel()

	type payload struct {
		Name  string `json:"name"`
		Count int    `json:"count"`
	}

	w := httptest.NewRecorder()
	writeJSON(w, payload{Name: "test", Count: 42})

	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))

	var got payload
	require.NoError(t, json.NewDecoder(w.Body).Decode(&got))
	assert.Equal(t, "test", got.Name)
	assert.Equal(t, 42, got.Count)
}

func TestWriteJSON_Slice(t *testing.T) {
	t.Parallel()

	w := httptest.NewRecorder()
	writeJSON(w, []string{"a", "b"})

	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))

	var got []string
	require.NoError(t, json.NewDecoder(w.Body).Decode(&got))
	assert.Equal(t, []string{"a", "b"}, got)
}

func TestNewMetricsConfig_Defaults(t *testing.T) {
	// Clear env vars that might be set.
	t.Setenv("GCP_PROJECT_ID", "")
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("ANTHROPIC_MODEL", "")

	cfg := newMetricsConfig()

	assert.Equal(t, "devpulseio", cfg.projectID)
	assert.Equal(t, "", cfg.anthropicKey)
	assert.Equal(t, defaultInsightsModel, cfg.model)
	assert.Equal(t, "devpulse-saas-serve", cfg.service)
	assert.Equal(t, "devpulse-saas-import", cfg.job)
	assert.Equal(t, "devpulse-saas-deeprep", cfg.deeprep)
	assert.Equal(t, "devpulse-saas-admin", cfg.admin)
	assert.Equal(t, "devpulseio:devpulse-saas-pg", cfg.dbID)
}

func TestNewMetricsConfig_CustomEnv(t *testing.T) {
	t.Setenv("GCP_PROJECT_ID", "my-project")
	t.Setenv("ANTHROPIC_API_KEY", "sk-test-key")
	t.Setenv("ANTHROPIC_MODEL", "claude-haiku-4-5-20251001")

	cfg := newMetricsConfig()

	assert.Equal(t, "my-project", cfg.projectID)
	assert.Equal(t, "sk-test-key", cfg.anthropicKey)
	assert.Equal(t, "claude-haiku-4-5-20251001", cfg.model)
	assert.Equal(t, "my-project:devpulse-saas-pg", cfg.dbID)
}

func TestHandleUpgrade_EmptyBody(t *testing.T) {
	t.Parallel()

	handler := handleUpgrade(nil)
	req := httptest.NewRequest(http.MethodPost, "/upgrade", strings.NewReader(""))
	w := httptest.NewRecorder()

	handler(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "invalid request body")
}

func TestHandleUpgrade_MissingUsername(t *testing.T) {
	t.Parallel()

	handler := handleUpgrade(nil)
	req := httptest.NewRequest(http.MethodPost, "/upgrade", strings.NewReader(`{"plan":"pro"}`))
	w := httptest.NewRecorder()

	handler(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "username is required")
}

func TestHandleUpgrade_InvalidPlan(t *testing.T) {
	t.Parallel()

	handler := handleUpgrade(nil)
	req := httptest.NewRequest(http.MethodPost, "/upgrade",
		strings.NewReader(`{"username":"foo","plan":"invalid"}`))
	w := httptest.NewRecorder()

	handler(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "invalid plan")
}

func TestHandleInvite_EmptyBody(t *testing.T) {
	t.Parallel()

	handler := handleInvite(nil)
	req := httptest.NewRequest(http.MethodPost, "/invite", strings.NewReader(""))
	w := httptest.NewRecorder()

	handler(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "invalid request body")
}

func TestHandleInvite_MissingUsername(t *testing.T) {
	t.Parallel()

	handler := handleInvite(nil)
	req := httptest.NewRequest(http.MethodPost, "/invite", strings.NewReader(`{"plan":"pro"}`))
	w := httptest.NewRecorder()

	handler(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "username is required")
}

func TestHandleInvite_InvalidPlan(t *testing.T) {
	t.Parallel()

	handler := handleInvite(nil)
	req := httptest.NewRequest(http.MethodPost, "/invite",
		strings.NewReader(`{"username":"foo","plan":"bogus"}`))
	w := httptest.NewRecorder()

	handler(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "invalid plan")
}

func TestHandleGetTenant_MissingUsername(t *testing.T) {
	t.Parallel()

	handler := handleGetTenant(nil)
	req := httptest.NewRequest(http.MethodGet, "/tenant", nil)
	w := httptest.NewRecorder()

	handler(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "username query parameter is required")
}

func TestFormatTimeSeries_ValidDoubles(t *testing.T) {
	t.Parallel()

	d := 3.14
	raw := `{
		"timeSeries": [{
			"metric": {"labels": {"code_class": "2xx"}},
			"points": [{
				"interval": {"startTime": "2025-04-05T10:00:00Z"},
				"value": {"doubleValue": 3.14}
			}]
		}]
	}`
	_ = d

	result := formatTimeSeries(raw)
	assert.Contains(t, result, "2xx")
	assert.Contains(t, result, "3.14")
}

func TestFormatTimeSeries_ValidInt64(t *testing.T) {
	t.Parallel()

	raw := `{
		"timeSeries": [{
			"metric": {"labels": {}},
			"points": [{
				"interval": {"startTime": "2025-04-05T10:00:00Z"},
				"value": {"int64Value": "42"}
			}]
		}]
	}`

	result := formatTimeSeries(raw)
	assert.Contains(t, result, "42.00")
}

func TestFormatTimeSeries_Empty(t *testing.T) {
	t.Parallel()

	raw := `{"timeSeries": []}`
	result := formatTimeSeries(raw)
	assert.Equal(t, "  (no data)", result)
}

func TestFormatTimeSeries_NoTimeSeries(t *testing.T) {
	t.Parallel()

	raw := `{}`
	result := formatTimeSeries(raw)
	assert.Equal(t, "  (no data)", result)
}

func TestFormatTimeSeries_ParseError(t *testing.T) {
	t.Parallel()

	result := formatTimeSeries("not json")
	assert.Equal(t, "  (parse error)", result)
}

func TestFormatTimeSeries_MultiplePoints(t *testing.T) {
	t.Parallel()

	raw := `{
		"timeSeries": [{
			"metric": {"labels": {}},
			"points": [
				{"interval": {"startTime": "2025-04-05T10:00:00Z"}, "value": {"doubleValue": 1.0}},
				{"interval": {"startTime": "2025-04-05T09:00:00Z"}, "value": {"doubleValue": 2.0}},
				{"interval": {"startTime": "2025-04-05T08:00:00Z"}, "value": {"doubleValue": 3.0}}
			]
		}]
	}`

	result := formatTimeSeries(raw)
	assert.Contains(t, result, "1.00")
	assert.Contains(t, result, "2.00")
	assert.Contains(t, result, "3.00")
}
