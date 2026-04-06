package data

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildInsightsPrompt(t *testing.T) {
	metrics := &InsightsMetrics{
		Summary: &InsightsSummary{BusFactor: 3, PonyFactor: 1, Contributors: 19},
	}
	prompt := buildInsightsPrompt(metrics, 3)
	assert.Contains(t, prompt, "bus_factor")
	assert.Contains(t, prompt, "3 weeks")
	assert.Contains(t, prompt, "DORA")
}

func TestParseInsightsResponse_ValidJSON(t *testing.T) {
	raw := `{"observations":[{"headline":"Test","detail":"detail"}],"actions":[{"headline":"Act","detail":"do it"}]}`
	result, err := parseInsightsResponse(raw)
	require.NoError(t, err)
	require.Len(t, result.Observations, 1)
	assert.Equal(t, "Test", result.Observations[0].Headline)
	require.Len(t, result.Actions, 1)
	assert.Equal(t, "Act", result.Actions[0].Headline)
}

func TestParseInsightsResponse_WithCodeFences(t *testing.T) {
	raw := "```json\n{\"observations\":[],\"actions\":[]}\n```"
	result, err := parseInsightsResponse(raw)
	require.NoError(t, err)
	assert.Empty(t, result.Observations)
}

func TestParseInsightsResponse_PlainCodeFence(t *testing.T) {
	raw := "```\n{\"observations\":[],\"actions\":[]}\n```"
	result, err := parseInsightsResponse(raw)
	require.NoError(t, err)
	assert.Empty(t, result.Observations)
}

func TestParseInsightsResponse_InvalidJSON(t *testing.T) {
	_, err := parseInsightsResponse("not json")
	require.Error(t, err)
}

func TestParseInsightsResponse_EmptyString(t *testing.T) {
	_, err := parseInsightsResponse("")
	require.Error(t, err)
}

func TestParseInsightsResponse_WhitespaceTrimmed(t *testing.T) {
	raw := "  \n{\"observations\":[{\"headline\":\"X\",\"detail\":\"Y\"}],\"actions\":[]}\n  "
	result, err := parseInsightsResponse(raw)
	require.NoError(t, err)
	require.Len(t, result.Observations, 1)
	assert.Equal(t, "X", result.Observations[0].Headline)
}

func TestGenerateInsights_Success(t *testing.T) {
	body := claudeResponse{
		Content: []claudeContentBlock{
			{Type: "text", Text: `{"observations":[{"headline":"Good","detail":"All well."}],"actions":[{"headline":"Act","detail":"Do it."}]}`},
		},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(body)
	}))
	defer srv.Close()

	cfg := &LLMConfig{Token: "test-key", BaseURL: srv.URL, Model: "test-model"}
	got, model, err := GenerateInsights(context.Background(), cfg, &InsightsMetrics{}, 3)
	require.NoError(t, err)
	assert.Equal(t, "test-model", model)
	require.Len(t, got.Observations, 1)
	assert.Equal(t, "Good", got.Observations[0].Headline)
}

func TestGenerateInsights_RetryOn429_ContextCancelled(t *testing.T) {
	// Verify that a 429 response followed by context cancellation returns ctx.Err().
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel immediately so the retry sleep is aborted.
	cancel()

	cfg := &LLMConfig{Token: "key", BaseURL: srv.URL, Model: "m"}
	_, _, err := GenerateInsights(ctx, cfg, &InsightsMetrics{}, 1)
	require.Error(t, err)
	// Either the request itself fails due to canceled ctx, or the retry select hits ctx.Done().
	// Either way we expect an error; call count may be 0 or 1 depending on timing.
	_ = calls.Load()
}

func TestGenerateInsights_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":"unauthorized"}`))
	}))
	defer srv.Close()

	cfg := &LLMConfig{Token: "bad-key", BaseURL: srv.URL}
	_, _, err := GenerateInsights(context.Background(), cfg, &InsightsMetrics{}, 3)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "401")
}

func TestNewLLMConfigFromEnv_NoKey(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("ANTHROPIC_MODEL", "")
	t.Setenv("ANTHROPIC_BASE_URL", "")

	cfg := NewLLMConfigFromEnv()
	assert.Nil(t, cfg)
}

func TestNewLLMConfigFromEnv_KeyOnly(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-test-123")
	t.Setenv("ANTHROPIC_MODEL", "")
	t.Setenv("ANTHROPIC_BASE_URL", "")

	cfg := NewLLMConfigFromEnv()
	require.NotNil(t, cfg)
	assert.Equal(t, "sk-test-123", cfg.Token)
	assert.Empty(t, cfg.Model)
	assert.Empty(t, cfg.BaseURL)
}

func TestNewLLMConfigFromEnv_AllSet(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-test-456")
	t.Setenv("ANTHROPIC_MODEL", "claude-haiku-4-5-20251001")
	t.Setenv("ANTHROPIC_BASE_URL", "https://custom.api.example.com")

	cfg := NewLLMConfigFromEnv()
	require.NotNil(t, cfg)
	assert.Equal(t, "sk-test-456", cfg.Token)
	assert.Equal(t, "claude-haiku-4-5-20251001", cfg.Model)
	assert.Equal(t, "https://custom.api.example.com", cfg.BaseURL)
}

func TestGenerateInsights_DefaultModel(t *testing.T) {
	body := claudeResponse{
		Content: []claudeContentBlock{
			{Type: "text", Text: `{"observations":[],"actions":[]}`},
		},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(body)
	}))
	defer srv.Close()

	cfg := &LLMConfig{Token: "key", BaseURL: srv.URL} // no model set
	_, model, err := GenerateInsights(context.Background(), cfg, &InsightsMetrics{}, 3)
	require.NoError(t, err)
	assert.Equal(t, DefaultInsightsModel, model)
}
