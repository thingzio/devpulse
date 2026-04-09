package data

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"strings"
	"time"

	"github.com/thingzio/devpulse/pkg/config"
)

const (
	claudeAPIURL         = "https://api.anthropic.com"
	claudeAPIVersion     = "2023-06-01"
	DefaultInsightsModel = "claude-haiku-4-5-20251001"
	insightsMaxTokens    = 4096
	insightsMaxRetries   = 3
	insightsRetryDelay   = 5 * time.Second
	insightsHTTPTimeout  = 30 * time.Second
)

var insightsHTTPClient = &http.Client{Timeout: insightsHTTPTimeout}

// LLMConfig holds configuration for the Claude API.
type LLMConfig struct {
	Token   string
	BaseURL string
	Model   string
}

// NewLLMConfigFromEnv creates an LLMConfig from environment variables.
// Returns nil if ANTHROPIC_API_KEY is not set.
func NewLLMConfigFromEnv() *LLMConfig {
	apiKey := config.AnthropicAPIKey()
	if apiKey == "" {
		return nil
	}
	return &LLMConfig{
		Token:   apiKey,
		BaseURL: config.AnthropicBaseURL(),
		Model:   config.AnthropicModel(DefaultInsightsModel),
	}
}

// InsightsMetrics holds all metrics gathered for a repo to feed into the LLM.
type InsightsMetrics struct {
	Summary          *InsightsSummary         `json:"summary"`
	Momentum         *MomentumSeries          `json:"momentum"`
	PRRatio          *PRReviewRatioSeries     `json:"pr_ratio"`
	TimeToMerge      *VelocitySeries          `json:"time_to_merge"`
	Retention        *RetentionSeries         `json:"retention"`
	Funnel           *ContributorFunnelSeries `json:"funnel"`
	ChangeFailure    *ChangeFailureRateSeries `json:"change_failure"`
	ReviewLatency    *ReviewLatencySeries     `json:"review_latency"`
	PRSize           *PRSizeSeries            `json:"pr_size"`
	ForksAndActivity *ForksAndActivitySeries  `json:"forks_and_activity"`
	RepoMeta         []*RepoMeta              `json:"repo_meta"`
	IssueRatio       *IssueRatioSeries        `json:"issue_ratio"`
	FirstResponse    *FirstResponseSeries     `json:"first_response"`
	ReleaseCadence   *ReleaseCadenceSeries    `json:"release_cadence"`
	ReleaseDownloads *ReleaseDownloadsSeries  `json:"release_downloads"`
	AgingPRs         *AgingPRsSeries          `json:"aging_prs"`
	Unanswered       *UnansweredSeries        `json:"unanswered"`
	ResponseSLO      *ResponseSLOSeries       `json:"response_slo"`
}

// GatherInsightsMetrics calls Store methods to collect all metrics for a repo.
// Individual metric failures are logged as warnings; the function always returns
// whatever metrics were successfully gathered.
func GatherInsightsMetrics(ctx context.Context, store Store, org, repo string, days int) *InsightsMetrics {
	o, r := &org, &repo
	m := &InsightsMetrics{}
	var err error

	if m.Summary, err = store.GetInsightsSummary(ctx, o, r, nil, days); err != nil {
		slog.Warn("insights: failed to get summary", "error", err)
	}
	if m.Momentum, err = store.GetContributorMomentum(ctx, o, r, nil, days); err != nil {
		slog.Warn("insights: failed to get momentum", "error", err)
	}
	if m.PRRatio, err = store.GetPRReviewRatio(ctx, o, r, nil, days); err != nil {
		slog.Warn("insights: failed to get pr review ratio", "error", err)
	}
	if m.TimeToMerge, err = store.GetTimeToMerge(ctx, o, r, nil, days); err != nil {
		slog.Warn("insights: failed to get time to merge", "error", err)
	}
	if m.Retention, err = store.GetContributorRetention(ctx, o, r, nil, days); err != nil {
		slog.Warn("insights: failed to get retention", "error", err)
	}
	if m.Funnel, err = store.GetContributorFunnel(ctx, o, r, nil, days); err != nil {
		slog.Warn("insights: failed to get funnel", "error", err)
	}
	if m.ChangeFailure, err = store.GetChangeFailureRate(ctx, o, r, nil, days); err != nil {
		slog.Warn("insights: failed to get change failure rate", "error", err)
	}
	if m.ReviewLatency, err = store.GetReviewLatency(ctx, o, r, nil, days); err != nil {
		slog.Warn("insights: failed to get review latency", "error", err)
	}
	if m.PRSize, err = store.GetPRSizeDistribution(ctx, o, r, nil, days); err != nil {
		slog.Warn("insights: failed to get pr size distribution", "error", err)
	}
	if m.ForksAndActivity, err = store.GetForksAndActivity(ctx, o, r, nil, days); err != nil {
		slog.Warn("insights: failed to get forks and activity", "error", err)
	}
	if m.IssueRatio, err = store.GetIssueOpenCloseRatio(ctx, o, r, nil, days); err != nil {
		slog.Warn("insights: failed to get issue ratio", "error", err)
	}
	if m.FirstResponse, err = store.GetTimeToFirstResponse(ctx, o, r, nil, days); err != nil {
		slog.Warn("insights: failed to get first response", "error", err)
	}
	if m.ReleaseCadence, err = store.GetReleaseCadence(ctx, o, r, nil, days); err != nil {
		slog.Warn("insights: failed to get release cadence", "error", err)
	}
	if m.ReleaseDownloads, err = store.GetReleaseDownloads(ctx, o, r, days); err != nil {
		slog.Warn("insights: failed to get release downloads", "error", err)
	}
	if m.RepoMeta, err = store.GetRepoMetas(ctx, o, r); err != nil {
		slog.Warn("insights: failed to get repo metas", "error", err)
	}
	if m.AgingPRs, err = store.GetAgingPRs(ctx, o, r, nil, days); err != nil {
		slog.Warn("insights: failed to get aging PRs", "error", err)
	}
	if m.Unanswered, err = store.GetUnansweredRate(ctx, o, r, nil, days); err != nil {
		slog.Warn("insights: failed to get unanswered rate", "error", err)
	}
	if m.ResponseSLO, err = store.GetResponseSLO(ctx, o, r, nil, days); err != nil {
		slog.Warn("insights: failed to get response SLO", "error", err)
	}

	return m
}

// buildInsightsPrompt assembles the JSON metrics and DORA benchmarks into a
// prompt suitable for the Claude Messages API.
func buildInsightsPrompt(metrics *InsightsMetrics, weeks int) string {
	metricsJSON, err := json.Marshal(metrics)
	if err != nil {
		slog.Warn("insights: failed to marshal metrics", "error", err)
		metricsJSON = []byte("{}")
	}

	var b strings.Builder
	fmt.Fprintf(&b, "You are an expert open-source project analyst. Analyze the following %d weeks of GitHub repository metrics and provide actionable insights.\n\n", weeks)
	fmt.Fprintf(&b, "## Metrics (JSON)\n\n%s\n\n", string(metricsJSON))
	b.WriteString("## DORA Benchmarks (for context)\n\n")
	b.WriteString("- Elite: Deployment frequency multiple times per day, lead time < 1 hour, change failure rate < 5%%, time to restore < 1 hour\n")
	b.WriteString("- High: Deployment frequency weekly to monthly, lead time 1 day to 1 week, change failure rate 5-10%, time to restore < 1 day\n")
	b.WriteString("- Medium: Deployment frequency monthly to every 6 months, lead time 1 week to 1 month, change failure rate 10-15%, time to restore 1 day to 1 week\n")
	b.WriteString("- Low: Deployment frequency > 6 months, lead time > 1 month, change failure rate > 15%, time to restore > 1 week\n\n")
	b.WriteString("## Instructions\n\n")
	b.WriteString("Provide exactly 5 observations and 3 recommended actions based on these metrics.\n")
	b.WriteString("Use third-person tone (e.g. \"the project shows\", \"this repository has\").\n")
	b.WriteString("Each observation and action must have a short headline and a 1-2 sentence detail.\n\n")
	b.WriteString("Respond with JSON only, no markdown, no explanation. Use this exact format:\n")
	b.WriteString(`{"observations":[{"headline":"...","detail":"..."}],"actions":[{"headline":"...","detail":"..."}]}`)

	return b.String()
}

// claudeRequest is the request body for the Claude Messages API.
type claudeRequest struct {
	Model     string          `json:"model"`
	MaxTokens int             `json:"max_tokens"`
	Messages  []claudeMessage `json:"messages"`
}

// claudeMessage is a single message in a Claude API request.
type claudeMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// claudeResponse is the response from the Claude Messages API.
type claudeResponse struct {
	Content []claudeContentBlock `json:"content"`
}

// claudeContentBlock is a single content block in a Claude API response.
type claudeContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// GenerateInsights calls the Claude Messages API to generate insights from
// the provided metrics. Returns the parsed insights, the model used, and any error.
func GenerateInsights(ctx context.Context, cfg *LLMConfig, metrics *InsightsMetrics, weeks int) (*GeneratedInsights, string, error) {
	model := cfg.Model
	if model == "" {
		model = DefaultInsightsModel
	}
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = claudeAPIURL
	}

	prompt := buildInsightsPrompt(metrics, weeks)

	reqBody := claudeRequest{
		Model:     model,
		MaxTokens: insightsMaxTokens,
		Messages: []claudeMessage{
			{Role: "user", Content: prompt},
		},
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, model, fmt.Errorf("marshal request: %w", err)
	}

	var respBytes []byte
	for attempt := range insightsMaxRetries {
		var req *http.Request
		req, err = http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/v1/messages", bytes.NewReader(bodyBytes))
		if err != nil {
			return nil, model, fmt.Errorf("create request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("x-api-key", cfg.Token)
		req.Header.Set("anthropic-version", claudeAPIVersion)

		var resp *http.Response
		resp, err = insightsHTTPClient.Do(req) //nolint:gosec,nolintlint // URL from trusted config
		if err != nil {
			return nil, model, fmt.Errorf("api call: %w", err)
		}

		respBytes, err = io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, model, fmt.Errorf("read response: %w", err)
		}

		if resp.StatusCode == http.StatusOK {
			break
		}

		if (resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == 529) && attempt < insightsMaxRetries-1 {
			jitter := time.Duration(rand.IntN(1000)) * time.Millisecond //nolint:gosec // non-crypto jitter ok for retry delay
			delay := insightsRetryDelay*time.Duration(attempt+1) + jitter
			slog.Warn("insights API retrying", "status", resp.StatusCode, "attempt", attempt+1, "delay", delay)
			select {
			case <-ctx.Done():
				return nil, model, ctx.Err()
			case <-time.After(delay):
				continue
			}
		}

		return nil, model, fmt.Errorf("api error (status %d): %s", resp.StatusCode, string(respBytes))
	}

	var cr claudeResponse
	if unmarshalErr := json.Unmarshal(respBytes, &cr); unmarshalErr != nil {
		return nil, model, fmt.Errorf("unmarshal response: %w", unmarshalErr)
	}

	if len(cr.Content) == 0 {
		return nil, model, fmt.Errorf("empty response content")
	}

	insights, err := parseInsightsResponse(cr.Content[0].Text)
	if err != nil {
		return nil, model, fmt.Errorf("parse insights: %w", err)
	}

	return insights, model, nil
}

// parseInsightsResponse extracts GeneratedInsights from the raw LLM text.
// It handles optional markdown code fences that Claude sometimes adds.
func parseInsightsResponse(text string) (*GeneratedInsights, error) {
	text = strings.TrimSpace(text)

	// Strip markdown code fences if present.
	if strings.HasPrefix(text, "```") {
		// Remove opening fence (e.g. "```json\n")
		if idx := strings.Index(text, "\n"); idx != -1 {
			text = text[idx+1:]
		}
		// Remove closing fence
		text = strings.TrimSuffix(text, "```")
		text = strings.TrimSpace(text)
	}

	var result GeneratedInsights
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		return nil, fmt.Errorf("invalid json: %w", err)
	}

	return &result, nil
}
