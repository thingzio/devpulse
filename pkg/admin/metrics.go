package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	monitoringBaseURL    = "https://monitoring.googleapis.com/v3/projects"
	metadataTokenURL     = "http://metadata.google.internal/computeMetadata/v1/instance/service-accounts/default/token" //nolint:gosec // GCP metadata URL, not a credential
	anthropicAPIURL      = "https://api.anthropic.com/v1/messages"
	anthropicAPIVersion  = "2023-06-01"
	defaultInsightsModel = "claude-sonnet-4-6"
	metricsHTTPTimeout   = 30 * time.Second
	analysisHTTPTimeout  = 90 * time.Second
	analysisMaxTokens    = 4096
	defaultDays          = 2
	maxDays              = 30
)

var (
	metricsClient  = &http.Client{Timeout: metricsHTTPTimeout}
	analysisClient = &http.Client{Timeout: analysisHTTPTimeout}
)

// metricsConfig holds config loaded once at handler creation.
type metricsConfig struct {
	projectID    string
	anthropicKey string
	model        string
	service      string
	job          string
	deeprep      string
	admin        string
	dbID         string
}

func newMetricsConfig() *metricsConfig {
	project := os.Getenv("GCP_PROJECT_ID")
	if project == "" {
		project = "devpulseio"
	}
	prefix := "devpulse-saas"
	model := os.Getenv("ANTHROPIC_MODEL")
	if model == "" {
		model = defaultInsightsModel
	}
	return &metricsConfig{
		projectID:    project,
		anthropicKey: os.Getenv("ANTHROPIC_API_KEY"),
		model:        model,
		service:      prefix + "-serve",
		job:          prefix + "-import",
		deeprep:      prefix + "-deeprep",
		admin:        prefix + "-admin",
		dbID:         project + ":" + prefix + "-pg",
	}
}

func handleMetricsReview(cfg *metricsConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if cfg.anthropicKey == "" {
			http.Error(w, "ANTHROPIC_API_KEY not configured", http.StatusServiceUnavailable)
			return
		}

		days := defaultDays
		if d := r.URL.Query().Get("days"); d != "" {
			if v, err := strconv.Atoi(d); err == nil && v > 0 && v <= maxDays {
				days = v
			}
		}

		token, err := gcpAccessToken(r.Context())
		if err != nil {
			slog.Error("getting GCP access token", "error", err)
			http.Error(w, fmt.Sprintf("failed to get GCP credentials: %v", err), http.StatusInternalServerError)
			return
		}

		metrics := collectAllMetrics(r.Context(), cfg, token, days)

		analysis, err := analyzeMetrics(r.Context(), cfg, metrics)
		if err != nil {
			slog.Error("analyzing metrics", "error", err)
			http.Error(w, fmt.Sprintf("failed to analyze metrics: %v", err), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprint(w, analysis)
	}
}

// gcpAccessToken fetches an access token from the metadata server.
func gcpAccessToken(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, metadataTokenURL, nil)
	if err != nil {
		return "", fmt.Errorf("creating token request: %w", err)
	}
	req.Header.Set("Metadata-Flavor", "Google")

	resp, err := metricsClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetching token: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("metadata server returned %d", resp.StatusCode)
	}

	var tok struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
		return "", fmt.Errorf("decoding token: %w", err)
	}
	return tok.AccessToken, nil
}

// queryTimeSeries calls the GCP Monitoring API for a single metric.
func queryTimeSeries(ctx context.Context, projectID, token, filter, params string, start, end time.Time) (string, error) {
	encoded := url.QueryEscape(filter)
	u := fmt.Sprintf("%s/%s/timeSeries?filter=%s&interval.startTime=%s&interval.endTime=%s&%s",
		monitoringBaseURL, projectID, encoded,
		start.Format(time.RFC3339), end.Format(time.RFC3339), params)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil) //nolint:gosec // URL from trusted config
	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := metricsClient.Do(req) //nolint:gosec // URL from trusted config
	if err != nil {
		return "", fmt.Errorf("querying metrics: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("monitoring API returned %d: %s", resp.StatusCode, string(body[:min(len(body), 200)]))
	}

	return string(body), nil
}

type metricQuery struct {
	label  string
	filter string
	params string
}

func collectAllMetrics(ctx context.Context, cfg *metricsConfig, token string, days int) string {
	now := time.Now().UTC()
	start := now.Add(-time.Duration(days) * 24 * time.Hour)

	hourlyAlign := "aggregation.alignmentPeriod=3600s"
	dailyAlign := fmt.Sprintf("aggregation.alignmentPeriod=%ds", days*86400)

	queries := []metricQuery{
		{
			label:  "Service: Request Count (req/s by response class)",
			filter: fmt.Sprintf(`resource.type="cloud_run_revision" AND resource.labels.service_name="%s" AND metric.type="run.googleapis.com/request_count"`, cfg.service),
			params: hourlyAlign + "&aggregation.perSeriesAligner=ALIGN_RATE&aggregation.crossSeriesReducer=REDUCE_SUM&aggregation.groupByFields=metric.labels.response_code_class",
		},
		{
			label:  "Service: Request Latency p50 (ms)",
			filter: fmt.Sprintf(`resource.type="cloud_run_revision" AND resource.labels.service_name="%s" AND metric.type="run.googleapis.com/request_latencies"`, cfg.service),
			params: hourlyAlign + "&aggregation.perSeriesAligner=ALIGN_PERCENTILE_50&aggregation.crossSeriesReducer=REDUCE_MEAN",
		},
		{
			label:  "Service: Request Latency p95 (ms)",
			filter: fmt.Sprintf(`resource.type="cloud_run_revision" AND resource.labels.service_name="%s" AND metric.type="run.googleapis.com/request_latencies"`, cfg.service),
			params: hourlyAlign + "&aggregation.perSeriesAligner=ALIGN_PERCENTILE_95&aggregation.crossSeriesReducer=REDUCE_MEAN",
		},
		{
			label:  "Service: Request Latency p99 (ms)",
			filter: fmt.Sprintf(`resource.type="cloud_run_revision" AND resource.labels.service_name="%s" AND metric.type="run.googleapis.com/request_latencies"`, cfg.service),
			params: hourlyAlign + "&aggregation.perSeriesAligner=ALIGN_PERCENTILE_99&aggregation.crossSeriesReducer=REDUCE_MEAN",
		},
		{
			label:  "Service: Instance Count",
			filter: fmt.Sprintf(`resource.type="cloud_run_revision" AND resource.labels.service_name="%s" AND metric.type="run.googleapis.com/container/instance_count"`, cfg.service),
			params: hourlyAlign + "&aggregation.perSeriesAligner=ALIGN_MAX&aggregation.crossSeriesReducer=REDUCE_SUM",
		},
		{
			label:  "Service: CPU Utilization",
			filter: fmt.Sprintf(`resource.type="cloud_run_revision" AND resource.labels.service_name="%s" AND metric.type="run.googleapis.com/container/cpu/utilizations"`, cfg.service),
			params: hourlyAlign + "&aggregation.perSeriesAligner=ALIGN_PERCENTILE_99&aggregation.crossSeriesReducer=REDUCE_MAX",
		},
		{
			label:  "Service: Memory Utilization",
			filter: fmt.Sprintf(`resource.type="cloud_run_revision" AND resource.labels.service_name="%s" AND metric.type="run.googleapis.com/container/memory/utilizations"`, cfg.service),
			params: hourlyAlign + "&aggregation.perSeriesAligner=ALIGN_PERCENTILE_99&aggregation.crossSeriesReducer=REDUCE_MAX",
		},
		{
			label:  "Import: Job Execution Results",
			filter: fmt.Sprintf(`resource.type="cloud_run_job" AND resource.labels.job_name="%s" AND metric.type="run.googleapis.com/job/completed_execution_count"`, cfg.job),
			params: hourlyAlign + "&aggregation.perSeriesAligner=ALIGN_DELTA&aggregation.crossSeriesReducer=REDUCE_SUM&aggregation.groupByFields=metric.labels.result",
		},
		{
			label:  "Import: Repo Errors (by org/repo)",
			filter: `metric.type="logging.googleapis.com/user/devpulse-saas-import-repo-errors"`,
			params: dailyAlign + "&aggregation.perSeriesAligner=ALIGN_SUM&aggregation.crossSeriesReducer=REDUCE_SUM&aggregation.groupByFields=metric.labels.org&aggregation.groupByFields=metric.labels.repo",
		},
		{
			label:  "Deep Reputation: Job Execution Results",
			filter: fmt.Sprintf(`resource.type="cloud_run_job" AND resource.labels.job_name="%s" AND metric.type="run.googleapis.com/job/completed_execution_count"`, cfg.deeprep),
			params: hourlyAlign + "&aggregation.perSeriesAligner=ALIGN_DELTA&aggregation.crossSeriesReducer=REDUCE_SUM&aggregation.groupByFields=metric.labels.result",
		},
		{
			label:  "Deep Reputation: Errors (by username)",
			filter: fmt.Sprintf(`metric.type="logging.googleapis.com/user/%s-deeprep-errors"`, "devpulse-saas"),
			params: dailyAlign + "&aggregation.perSeriesAligner=ALIGN_SUM&aggregation.crossSeriesReducer=REDUCE_SUM&aggregation.groupByFields=metric.labels.username",
		},
		{
			label:  "Cloud SQL: CPU Utilization",
			filter: fmt.Sprintf(`resource.type="cloudsql_database" AND resource.labels.database_id="%s" AND metric.type="cloudsql.googleapis.com/database/cpu/utilization"`, cfg.dbID),
			params: hourlyAlign + "&aggregation.perSeriesAligner=ALIGN_MEAN",
		},
		{
			label:  "Cloud SQL: Memory Utilization",
			filter: fmt.Sprintf(`resource.type="cloudsql_database" AND resource.labels.database_id="%s" AND metric.type="cloudsql.googleapis.com/database/memory/utilization"`, cfg.dbID),
			params: hourlyAlign + "&aggregation.perSeriesAligner=ALIGN_MEAN",
		},
		{
			label:  "Cloud SQL: Connections",
			filter: fmt.Sprintf(`resource.type="cloudsql_database" AND resource.labels.database_id="%s" AND metric.type="cloudsql.googleapis.com/database/postgresql/num_backends"`, cfg.dbID),
			params: hourlyAlign + "&aggregation.perSeriesAligner=ALIGN_MEAN",
		},
		{
			label:  "Cloud SQL: Transactions/sec",
			filter: fmt.Sprintf(`resource.type="cloudsql_database" AND resource.labels.database_id="%s" AND metric.type="cloudsql.googleapis.com/database/postgresql/transaction_count"`, cfg.dbID),
			params: hourlyAlign + "&aggregation.perSeriesAligner=ALIGN_RATE&aggregation.crossSeriesReducer=REDUCE_SUM",
		},
		{
			label:  "Cloud SQL: Deadlocks",
			filter: fmt.Sprintf(`resource.type="cloudsql_database" AND resource.labels.database_id="%s" AND metric.type="cloudsql.googleapis.com/database/postgresql/deadlock_count"`, cfg.dbID),
			params: hourlyAlign + "&aggregation.perSeriesAligner=ALIGN_DELTA&aggregation.crossSeriesReducer=REDUCE_SUM",
		},
		{
			label:  "Sign-ins (daily)",
			filter: `metric.type="logging.googleapis.com/user/devpulse-saas-sign-ins"`,
			params: "aggregation.alignmentPeriod=86400s&aggregation.perSeriesAligner=ALIGN_SUM&aggregation.crossSeriesReducer=REDUCE_SUM",
		},
		{
			label: "Application Errors (hourly)",
			filter: fmt.Sprintf(
				`resource.type="cloud_run_revision" AND resource.labels.service_name="%s"`+
					` AND metric.type="logging.googleapis.com/log_entry_count" AND metric.labels.severity="ERROR"`,
				cfg.service),
			params: hourlyAlign + "&aggregation.perSeriesAligner=ALIGN_SUM&aggregation.crossSeriesReducer=REDUCE_SUM",
		},
		{
			label:  "Admin: Request Count",
			filter: fmt.Sprintf(`resource.type="cloud_run_revision" AND resource.labels.service_name="%s" AND metric.type="run.googleapis.com/request_count"`, cfg.admin),
			params: hourlyAlign + "&aggregation.perSeriesAligner=ALIGN_RATE&aggregation.crossSeriesReducer=REDUCE_SUM&aggregation.groupByFields=metric.labels.response_code_class",
		},
	}

	var b strings.Builder
	fmt.Fprintf(&b, "DevPulse Metrics — Last %d day(s), Project: %s, %s\n\n",
		days, cfg.projectID, now.Format("2006-01-02 15:04 UTC"))

	for _, q := range queries {
		raw, err := queryTimeSeries(ctx, cfg.projectID, token, q.filter, q.params, start, now)
		if err != nil {
			fmt.Fprintf(&b, "--- %s ---\n  (error: %s)\n\n", q.label, err)
			continue
		}
		fmt.Fprintf(&b, "--- %s ---\n%s\n\n", q.label, formatTimeSeries(raw))
	}

	return b.String()
}

// formatTimeSeries extracts data points from the monitoring API JSON response.
func formatTimeSeries(raw string) string {
	var resp struct {
		TimeSeries []struct {
			Metric struct {
				Labels map[string]string `json:"labels"`
			} `json:"metric"`
			Points []struct {
				Interval struct {
					StartTime string `json:"startTime"`
				} `json:"interval"`
				Value struct {
					DoubleValue *float64 `json:"doubleValue"`
					Int64Value  *string  `json:"int64Value"`
				} `json:"value"`
			} `json:"points"`
		} `json:"timeSeries"`
	}

	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		return "  (parse error)"
	}

	if len(resp.TimeSeries) == 0 {
		return "  (no data)"
	}

	var b strings.Builder
	for _, ts := range resp.TimeSeries {
		var prefix string
		if len(ts.Metric.Labels) > 0 {
			var parts []string
			for _, v := range ts.Metric.Labels {
				parts = append(parts, v)
			}
			prefix = strings.Join(parts, " ") + ": "
		}

		limit := 8
		if len(ts.Points) < limit {
			limit = len(ts.Points)
		}
		var vals []string
		for _, p := range ts.Points[:limit] {
			ts := p.Interval.StartTime
			if len(ts) > 16 {
				ts = ts[5:16]
			}
			var v float64
			if p.Value.DoubleValue != nil {
				v = *p.Value.DoubleValue
			} else if p.Value.Int64Value != nil {
				if iv, err := strconv.ParseFloat(*p.Value.Int64Value, 64); err == nil {
					v = iv
				}
			}
			vals = append(vals, fmt.Sprintf("%s=%.2f", ts, v))
		}
		fmt.Fprintf(&b, "  %s%s\n", prefix, strings.Join(vals, ", "))
	}
	return b.String()
}

// analyzeMetrics sends the collected metrics to the Anthropic API for analysis.
func analyzeMetrics(ctx context.Context, cfg *metricsConfig, metrics string) (string, error) {
	systemPrompt := `You are a DevOps analyst reviewing GCP infrastructure metrics for DevPulse, a multi-tenant SaaS running on Cloud Run + Cloud SQL PostgreSQL. Analyze the raw metrics and provide:

1. **Key Observations** — What stands out? Anomalies, trends, threshold breaches.
2. **Risks** — Anything approaching danger zones (CPU > 80%, latency p99 > 500ms, errors, deadlocks, disk growth).
3. **Recommended Actions** — Concrete steps to address any issues found.

Be concise. Use bullet points. Skip metrics that look normal. If everything looks healthy, say so briefly.`

	body, err := json.Marshal(map[string]any{
		"model":      cfg.model,
		"max_tokens": analysisMaxTokens,
		"system":     systemPrompt,
		"messages":   []map[string]string{{"role": "user", "content": metrics}},
	})
	if err != nil {
		return "", fmt.Errorf("marshaling request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, anthropicAPIURL, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", cfg.anthropicKey)
	req.Header.Set("anthropic-version", anthropicAPIVersion)

	resp, err := analysisClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("calling Anthropic API: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("anthropic API returned %d: %s", resp.StatusCode, string(respBody[:min(len(respBody), 200)]))
	}

	var cr struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(respBody, &cr); err != nil {
		return "", fmt.Errorf("parsing response: %w", err)
	}

	if len(cr.Content) == 0 {
		return "", fmt.Errorf("empty response from Anthropic API")
	}

	return cr.Content[0].Text, nil
}
