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
	"strconv"
	"strings"
	"time"

	"github.com/thingzio/devpulse/pkg/config"
)

const (
	monitoringBaseURL    = "https://monitoring.googleapis.com/v3/projects"
	metadataTokenURL     = "http://metadata.google.internal/computeMetadata/v1/instance/service-accounts/default/token" //nolint:gosec // GCP metadata URL, not a credential
	anthropicAPIURL      = "https://api.anthropic.com/v1/messages"
	anthropicAPIVersion  = "2023-06-01"
	defaultInsightsModel = "claude-sonnet-4-6"
	metricsHTTPTimeout   = 30 * time.Second
	analysisHTTPTimeout  = 90 * time.Second
	analysisMaxTokens    = 2048
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
	admin        string
	dbID         string
}

func newMetricsConfig() *metricsConfig {
	project := config.GCPProjectID()
	prefix := "devpulse-saas"
	return &metricsConfig{
		projectID:    project,
		anthropicKey: config.AnthropicAPIKey(),
		model:        config.AnthropicModel(defaultInsightsModel),
		service:      prefix + "-serve",
		job:          prefix + "-import",
		admin:        prefix + "-admin",
		dbID:         project + ":" + prefix + "-pg",
	}
}

func handleMetricsReview(cfg *metricsConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if cfg.anthropicKey == "" {
			http.Error(w, "anthropic API key not configured", http.StatusServiceUnavailable)
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
			slog.Error("failed to get GCP credentials", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		metrics := collectAllMetrics(r.Context(), cfg, token, days)

		analysis, err := analyzeMetrics(r.Context(), cfg, metrics)
		if err != nil {
			slog.Error("failed to analyze metrics", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
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

func metricQueries(cfg *metricsConfig, hourlyAlign, dailyAlign string) []metricQuery {
	return []metricQuery{
		// --- Service ---
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
			label:  "Service: Startup Latency (ms, cold starts)",
			filter: fmt.Sprintf(`resource.type="cloud_run_revision" AND resource.labels.service_name="%s" AND metric.type="run.googleapis.com/container/startup_latencies"`, cfg.service),
			params: hourlyAlign + "&aggregation.perSeriesAligner=ALIGN_PERCENTILE_99&aggregation.crossSeriesReducer=REDUCE_MAX",
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
			label:  "Service: Billable Instance Time (s/s)",
			filter: fmt.Sprintf(`resource.type="cloud_run_revision" AND resource.labels.service_name="%s" AND metric.type="run.googleapis.com/container/billable_instance_time"`, cfg.service),
			params: hourlyAlign + "&aggregation.perSeriesAligner=ALIGN_RATE&aggregation.crossSeriesReducer=REDUCE_SUM",
		},
		// --- Import ---
		{
			label:  "Import: Duration (seconds)",
			filter: `metric.type="logging.googleapis.com/user/devpulse-saas-import-duration"`,
			params: hourlyAlign + "&aggregation.perSeriesAligner=ALIGN_DELTA&aggregation.crossSeriesReducer=REDUCE_MEAN",
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
			label:  "Import: Backfill Rate Limited (by repo)",
			filter: `metric.type="logging.googleapis.com/user/devpulse-saas-backfill-rate-limited"`,
			params: dailyAlign + "&aggregation.perSeriesAligner=ALIGN_SUM&aggregation.crossSeriesReducer=REDUCE_SUM&aggregation.groupByFields=metric.labels.repo",
		},
		{
			label:  "Import: PR Backfill Completed (updated count)",
			filter: `metric.type="logging.googleapis.com/user/devpulse-saas-backfill-completed"`,
			params: hourlyAlign + "&aggregation.perSeriesAligner=ALIGN_SUM&aggregation.crossSeriesReducer=REDUCE_SUM",
		},
		// --- Cloud SQL ---
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
			label:  "Cloud SQL: Disk Utilization",
			filter: fmt.Sprintf(`resource.type="cloudsql_database" AND resource.labels.database_id="%s" AND metric.type="cloudsql.googleapis.com/database/disk/utilization"`, cfg.dbID),
			params: hourlyAlign + "&aggregation.perSeriesAligner=ALIGN_MEAN",
		},
		{
			label:  "Cloud SQL: Disk Used (bytes)",
			filter: fmt.Sprintf(`resource.type="cloudsql_database" AND resource.labels.database_id="%s" AND metric.type="cloudsql.googleapis.com/database/disk/bytes_used"`, cfg.dbID),
			params: hourlyAlign + "&aggregation.perSeriesAligner=ALIGN_MEAN",
		},
		// --- User Engagement ---
		{
			label:  "Sign-ins (daily)",
			filter: `metric.type="logging.googleapis.com/user/devpulse-saas-sign-ins"`,
			params: "aggregation.alignmentPeriod=86400s&aggregation.perSeriesAligner=ALIGN_SUM&aggregation.crossSeriesReducer=REDUCE_SUM",
		},
		{
			label:  "ToS Accepted (daily)",
			filter: `metric.type="logging.googleapis.com/user/devpulse-saas-tos-accepted"`,
			params: "aggregation.alignmentPeriod=86400s&aggregation.perSeriesAligner=ALIGN_SUM&aggregation.crossSeriesReducer=REDUCE_SUM",
		},
		{
			label:  "Upgrade Requests (daily)",
			filter: `metric.type="logging.googleapis.com/user/devpulse-saas-upgrade-requests"`,
			params: "aggregation.alignmentPeriod=86400s&aggregation.perSeriesAligner=ALIGN_SUM&aggregation.crossSeriesReducer=REDUCE_SUM",
		},
		{
			label:  "Webhook: Installation Events (daily)",
			filter: `metric.type="logging.googleapis.com/user/devpulse-saas-webhook-installs"`,
			params: "aggregation.alignmentPeriod=86400s&aggregation.perSeriesAligner=ALIGN_SUM&aggregation.crossSeriesReducer=REDUCE_SUM",
		},
		{
			label:  "Event Limit Reached (daily)",
			filter: `metric.type="logging.googleapis.com/user/devpulse-saas-event-limit-reached"`,
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
		// --- Admin ---
		{
			label:  "Admin: Request Count",
			filter: fmt.Sprintf(`resource.type="cloud_run_revision" AND resource.labels.service_name="%s" AND metric.type="run.googleapis.com/request_count"`, cfg.admin),
			params: hourlyAlign + "&aggregation.perSeriesAligner=ALIGN_RATE&aggregation.crossSeriesReducer=REDUCE_SUM&aggregation.groupByFields=metric.labels.response_code_class",
		},
		{
			label:  "Admin: Request Latency p99 (ms)",
			filter: fmt.Sprintf(`resource.type="cloud_run_revision" AND resource.labels.service_name="%s" AND metric.type="run.googleapis.com/request_latencies"`, cfg.admin),
			params: hourlyAlign + "&aggregation.perSeriesAligner=ALIGN_PERCENTILE_99&aggregation.crossSeriesReducer=REDUCE_MEAN",
		},
	}
}

// trendDays is the lookback window for the trend/baseline comparison section.
const trendDays = 7

// trendQueries returns a subset of high-signal metrics for trend comparison.
// These use daily alignment to give one data point per day over the trend window.
func trendQueries(cfg *metricsConfig) []metricQuery {
	daily := "aggregation.alignmentPeriod=86400s"
	return []metricQuery{
		{
			label:  "Service: Request Count (daily total)",
			filter: fmt.Sprintf(`resource.type="cloud_run_revision" AND resource.labels.service_name="%s" AND metric.type="run.googleapis.com/request_count"`, cfg.service),
			params: daily + "&aggregation.perSeriesAligner=ALIGN_DELTA&aggregation.crossSeriesReducer=REDUCE_SUM",
		},
		{
			label:  "Service: Request Latency p99 (daily max, ms)",
			filter: fmt.Sprintf(`resource.type="cloud_run_revision" AND resource.labels.service_name="%s" AND metric.type="run.googleapis.com/request_latencies"`, cfg.service),
			params: daily + "&aggregation.perSeriesAligner=ALIGN_PERCENTILE_99&aggregation.crossSeriesReducer=REDUCE_MAX",
		},
		{
			label:  "Import: Job Execution Results (daily)",
			filter: fmt.Sprintf(`resource.type="cloud_run_job" AND resource.labels.job_name="%s" AND metric.type="run.googleapis.com/job/completed_execution_count"`, cfg.job),
			params: daily + "&aggregation.perSeriesAligner=ALIGN_DELTA&aggregation.crossSeriesReducer=REDUCE_SUM&aggregation.groupByFields=metric.labels.result",
		},
		{
			label:  "Import: Repo Errors (daily total)",
			filter: `metric.type="logging.googleapis.com/user/devpulse-saas-import-repo-errors"`,
			params: daily + "&aggregation.perSeriesAligner=ALIGN_SUM&aggregation.crossSeriesReducer=REDUCE_SUM",
		},
		{
			label:  "Cloud SQL: CPU Utilization (daily avg)",
			filter: fmt.Sprintf(`resource.type="cloudsql_database" AND resource.labels.database_id="%s" AND metric.type="cloudsql.googleapis.com/database/cpu/utilization"`, cfg.dbID),
			params: daily + "&aggregation.perSeriesAligner=ALIGN_MEAN",
		},
		{
			label:  "Cloud SQL: Connections (daily max)",
			filter: fmt.Sprintf(`resource.type="cloudsql_database" AND resource.labels.database_id="%s" AND metric.type="cloudsql.googleapis.com/database/postgresql/num_backends"`, cfg.dbID),
			params: daily + "&aggregation.perSeriesAligner=ALIGN_MAX",
		},
		{
			label:  "Sign-ins (daily)",
			filter: `metric.type="logging.googleapis.com/user/devpulse-saas-sign-ins"`,
			params: daily + "&aggregation.perSeriesAligner=ALIGN_SUM&aggregation.crossSeriesReducer=REDUCE_SUM",
		},
		{
			label: "Application Errors (daily total)",
			filter: fmt.Sprintf(
				`resource.type="cloud_run_revision" AND resource.labels.service_name="%s"`+
					` AND metric.type="logging.googleapis.com/log_entry_count" AND metric.labels.severity="ERROR"`,
				cfg.service),
			params: daily + "&aggregation.perSeriesAligner=ALIGN_SUM&aggregation.crossSeriesReducer=REDUCE_SUM",
		},
	}
}

func collectAllMetrics(ctx context.Context, cfg *metricsConfig, token string, days int) string {
	now := time.Now().UTC()
	start := now.Add(-time.Duration(days) * 24 * time.Hour)

	hourlyAlign := "aggregation.alignmentPeriod=3600s"
	dailyAlign := fmt.Sprintf("aggregation.alignmentPeriod=%ds", days*86400)

	var b strings.Builder
	fmt.Fprintf(&b, "DevPulse Metrics — Last %d day(s), Project: %s, %s\n\n",
		days, cfg.projectID, now.Format("2006-01-02 15:04 UTC"))

	for _, q := range metricQueries(cfg, hourlyAlign, dailyAlign) {
		raw, err := queryTimeSeries(ctx, cfg.projectID, token, q.filter, q.params, start, now)
		if err != nil {
			fmt.Fprintf(&b, "--- %s ---\n  (error: %s)\n\n", q.label, err)
			continue
		}
		fmt.Fprintf(&b, "--- %s ---\n%s\n\n", q.label, formatTimeSeries(raw))
	}

	// Append 7-day trend data when the primary window is shorter.
	if days < trendDays {
		trendStart := now.Add(-time.Duration(trendDays) * 24 * time.Hour)
		fmt.Fprintf(&b, "=== 7-Day Trend Baseline ===\n\n")
		for _, q := range trendQueries(cfg) {
			raw, err := queryTimeSeries(ctx, cfg.projectID, token, q.filter, q.params, trendStart, now)
			if err != nil {
				fmt.Fprintf(&b, "--- %s ---\n  (error: %s)\n\n", q.label, err)
				continue
			}
			fmt.Fprintf(&b, "--- %s ---\n%s\n\n", q.label, formatTimeSeries(raw))
		}
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

		limit := min(8, len(ts.Points))
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
	systemPrompt := `You are a DevOps analyst reviewing GCP infrastructure metrics
for DevPulse, a multi-tenant SaaS on Cloud Run + Cloud SQL PostgreSQL.

## Architecture
- **Serve service** (devpulse-saas-serve): HTTP server, min_instance_count=0 (scale-to-zero).
- **Import job** (devpulse-saas-import): Batch worker, runs every 2 hours via Cloud Scheduler.
  Each execution uses 3 tasks × 2 goroutine workers. Imports events, runs deep reputation
  analysis inline, then generates LLM insights per repo.
- **Admin service** (devpulse-saas-admin): IAM-gated, scale-to-zero, writeTimeout=90s.
- **Shared token pool**: Import mints GitHub installation tokens from ALL active installations
  across ALL tenants, deduplicates by installation ID, round-robin with pre-flight quota check
  (skips tokens with <100 remaining). Tokens auto-skip when exhausted via Exhaust()/ActiveCount().
- **PR Backfill**: Bounded to last 90 days, 500 PRs/run max, newest-first. 0 updated PRs is
  normal when all recent PRs already have size data — this is NOT a stall.

## Known Baselines & Thresholds
- DB connection pools: serve=17max/5idle, import=5max/2idle, admin=3max/1idle.
- Alert thresholds: p99 latency >1s for 600s, 5xx >5/min for 5min, DB CPU >80% for 5min,
  DB connections >80 for 5min, cold-start latency >3s.
- Admin writeTimeout is 90s — responses up to ~85s are allowed but concerning.
- GitHub rate limit: per-installation, 60-min sliding window (NOT top-of-hour reset).

## User Engagement Metrics Context
- Sign-ins, ToS Accepted, Upgrade Requests, Webhook Installs, Event Limit Reached are
  business signals. Correlate them with load metrics (e.g. latency spikes during sign-in bursts).
- "Event Limit Reached" means a tenant hit their weekly event import cap — indicates plan friction.
- "Upgrade Requests" means a tenant clicked the upgrade button — revenue signal.

## Output Format
Respond with clean HTML suitable for embedding in an email body. Use only inline styles.
Use <h4> for section headings, <ul>/<li> for bullet points, <strong> for emphasis.
Do NOT use Markdown. Do NOT wrap output in <html>, <head>, or <body> tags.

## Analysis Instructions
Provide a brief, actionable analysis in three sections:

1. <h4>Key Observations</h4> — Only anomalies, threshold breaches, or notable trends.
   One bullet per finding. Correlate across categories (e.g. latency + import timing).
   Skip anything that looks normal.
2. <h4>Risks</h4> — Rate each: 🔴 critical, 🟠 warning, 🟡 watch.
   Only flag metrics abnormal relative to the baselines above. If none, say "No risks identified."
3. <h4>Actions</h4> — Concrete next steps only if risks were found. One line each.
   Do NOT recommend features that already exist (token pool retry, connection pool bounds, backfill limits).

If a "7-Day Trend Baseline" section is present, compare today's metrics against the trailing
7-day pattern. Flag regressions and improvements inline within Key Observations.

Target length: 10-15 bullet points total. If everything looks healthy, say so in 2-3 sentences.`

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
