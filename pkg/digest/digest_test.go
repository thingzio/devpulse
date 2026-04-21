package digest

import (
	"html/template"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thingzio/devpulse/pkg/data"
)

func TestDigestDay_Deterministic(t *testing.T) {
	id := "550e8400-e29b-41d4-a716-446655440000"
	d1 := digestDay(id)
	d2 := digestDay(id)
	assert.Equal(t, d1, d2)
	assert.GreaterOrEqual(t, d1, 0)
	assert.LessOrEqual(t, d1, 6)
}

func TestDigestDay_Distribution(t *testing.T) {
	counts := make(map[int]int)
	for i := range 100 {
		id := "tenant-" + string(rune('a'+i%26)) + string(rune('0'+i/26))
		counts[digestDay(id)]++
	}
	for day := range 7 {
		assert.Greater(t, counts[day], 0, "day %d should have at least one tenant", day)
	}
}

func TestUnsubscribeToken_RoundTrip(t *testing.T) {
	secret := "test-secret-key"
	tenantID := "550e8400-e29b-41d4-a716-446655440000"
	token := UnsubscribeToken(secret, tenantID)
	require.NotEmpty(t, token)
	assert.True(t, ValidateUnsubscribeToken(secret, tenantID, token))
}

func TestUnsubscribeToken_WrongSecret(t *testing.T) {
	tenantID := "550e8400-e29b-41d4-a716-446655440000"
	token := UnsubscribeToken("secret-a", tenantID)
	assert.False(t, ValidateUnsubscribeToken("secret-b", tenantID, token))
}

func TestUnsubscribeToken_WrongTenant(t *testing.T) {
	secret := "test-secret"
	token := UnsubscribeToken(secret, "tenant-a")
	assert.False(t, ValidateUnsubscribeToken(secret, "tenant-b", token))
}

func TestRenderHTML_WithData(t *testing.T) {
	ps := &data.PortfolioSummary{
		TotalStars:        1500,
		TotalForks:        300,
		TotalOpenIssues:   42,
		TotalContributors: 85,
		StarsDelta:        25,
		StarsDeltaPct:     1.7,
		ForksDelta:        -3,
		ForksDeltaPct:     -1.0,
		MedianMergeHours:  18.5,
	}
	signals := []*data.Signal{
		{Org: "org", Repo: "repo1", Metric: "events", Message: "Events up 40%", Severity: "info"},
		{Org: "org", Repo: "repo2", Metric: "prs", Message: "PR velocity down", Severity: "warning"},
	}

	html := renderHTML(ps, signals, "https://devpulse.thingz.io", "https://devpulse.thingz.io/unsub")
	assert.Contains(t, html, "1500")
	assert.Contains(t, html, "+25")
	assert.Contains(t, html, "Events up 40%")
	assert.Contains(t, html, "PR velocity down")
	assert.Contains(t, html, "Unsubscribe")
	assert.Contains(t, html, "View Dashboard")
}

func TestRenderHTML_NilPortfolio(t *testing.T) {
	html := renderHTML(nil, nil, "https://example.com", "https://example.com/unsub")
	assert.Contains(t, html, "DevPulse Weekly Digest")
	assert.Contains(t, html, "Unsubscribe")
}

func TestRenderText_WithData(t *testing.T) {
	ps := &data.PortfolioSummary{
		TotalStars:        100,
		TotalContributors: 10,
		MedianMergeHours:  5.2,
	}
	signals := []*data.Signal{
		{Org: "o", Repo: "r", Metric: "m", Message: "test signal", Severity: "info"},
	}

	text := renderText(ps, signals, "https://dash.example.com", "https://unsub.example.com")
	assert.Contains(t, text, "PORTFOLIO PULSE")
	assert.Contains(t, text, "Stars:        100")
	assert.Contains(t, text, "test signal")
	assert.Contains(t, text, "Unsubscribe")
}

func TestFormatDelta(t *testing.T) {
	assert.Equal(t, "+25 (1.7%)", formatDelta(25, 1.7))
	assert.Equal(t, "-3 (-1.0%)", formatDelta(-3, -1.0))
	assert.Equal(t, "", formatDelta(0, 0))
}

func TestNewConfigFromEnv_Missing(t *testing.T) {
	t.Setenv("SEND_API_KEY", "")
	assert.Nil(t, NewConfigFromEnv())
}

func TestNewConfigFromEnv_MissingBaseURL(t *testing.T) {
	t.Setenv("SEND_API_KEY", "re_test")
	t.Setenv("BASE_URL", "")
	assert.Nil(t, NewConfigFromEnv())
}

func TestNewConfigFromEnv_AdminOnly(t *testing.T) {
	t.Setenv("SEND_API_KEY", "re_test_key")
	t.Setenv("BASE_URL", "https://devpulse.example.com")
	t.Setenv("DIGEST_HMAC_SECRET", "my-secret")
	t.Setenv("DEVPULSE_ADMIN_USERS", "testuser,other")
	t.Setenv("DIGEST_ADMIN_ONLY", "true")

	cfg := NewConfigFromEnv()
	require.NotNil(t, cfg)
	assert.Equal(t, "re_test_key", cfg.ResendAPIKey)
	assert.Equal(t, "https://devpulse.example.com", cfg.BaseURL)
	assert.Equal(t, "my-secret", cfg.HMACSecret)
	assert.Equal(t, "testuser", cfg.TestUsername)
}

func TestNewConfigFromEnv_AllUsers(t *testing.T) {
	t.Setenv("SEND_API_KEY", "re_test_key")
	t.Setenv("BASE_URL", "https://devpulse.example.com")
	t.Setenv("DIGEST_HMAC_SECRET", "my-secret")
	t.Setenv("DEVPULSE_ADMIN_USERS", "testuser")
	t.Setenv("DIGEST_ADMIN_ONLY", "false")

	cfg := NewConfigFromEnv()
	require.NotNil(t, cfg)
	assert.Empty(t, cfg.TestUsername, "should send to all users when admin-only is false")
}

func TestNewConfigFromEnv_MissingSecret(t *testing.T) {
	t.Setenv("SEND_API_KEY", "re_api_key")
	t.Setenv("BASE_URL", "https://example.com")
	t.Setenv("DIGEST_HMAC_SECRET", "")

	assert.Nil(t, NewConfigFromEnv(), "should return nil when DIGEST_HMAC_SECRET is missing")
}

func TestHMACSecret(t *testing.T) {
	t.Setenv("DIGEST_HMAC_SECRET", "my-hmac-key")
	assert.Equal(t, "my-hmac-key", HMACSecret())
}

func TestHMACSecret_Empty(t *testing.T) {
	t.Setenv("DIGEST_HMAC_SECRET", "")
	assert.Empty(t, HMACSecret())
}

func TestRenderHTML_SeverityColors(t *testing.T) {
	signals := []*data.Signal{
		{Org: "o", Repo: "r", Metric: "m", Message: "info msg", Severity: "info"},
		{Org: "o", Repo: "r", Metric: "m", Message: "warn msg", Severity: "warning"},
		{Org: "o", Repo: "r", Metric: "m", Message: "crit msg", Severity: "critical"},
	}

	html := renderHTML(nil, signals, "https://example.com", "https://example.com/unsub")
	assert.Contains(t, html, "#4ade80") // info = green
	assert.Contains(t, html, "#ef4444") // critical = red
	assert.Contains(t, html, "info msg")
	assert.Contains(t, html, "warn msg")
	assert.Contains(t, html, "crit msg")
}

func TestRenderText_NoData(t *testing.T) {
	text := renderText(nil, nil, "https://dash.example.com", "https://unsub.example.com")
	assert.Contains(t, text, "DevPulse Weekly Digest")
	assert.NotContains(t, text, "PORTFOLIO PULSE")
	assert.NotContains(t, text, "NOTABLE CHANGES")
	assert.Contains(t, text, "View Dashboard")
}

func TestRenderText_NoMergeHours(t *testing.T) {
	ps := &data.PortfolioSummary{
		TotalStars:        50,
		TotalContributors: 3,
		MedianMergeHours:  0,
	}
	text := renderText(ps, nil, "https://example.com", "https://example.com/unsub")
	assert.NotContains(t, text, "Median Merge")
}

func TestBuildKPIs(t *testing.T) {
	ps := &data.PortfolioSummary{
		TotalStars:        100,
		TotalForks:        50,
		TotalOpenIssues:   10,
		TotalContributors: 5,
		StarsDelta:        5,
		StarsDeltaPct:     2.0,
		MedianMergeHours:  3.5,
	}
	kpis := buildKPIs(ps)
	assert.Len(t, kpis, 5)
	assert.Equal(t, "Stars", kpis[0].Label)
	assert.Equal(t, "100", kpis[0].Value)
	assert.Equal(t, template.HTML("+5 (2.0%)"), kpis[0].Delta)
	assert.Equal(t, "Median Merge Time", kpis[4].Label)
}

func TestBuildKPIs_NoMergeHours(t *testing.T) {
	ps := &data.PortfolioSummary{TotalStars: 10}
	kpis := buildKPIs(ps)
	assert.Len(t, kpis, 4)
}

func TestSeverityColor(t *testing.T) {
	assert.Equal(t, "#ef4444", severityColor("critical"))
	assert.Equal(t, "#f59e0b", severityColor("warning"))
	assert.Equal(t, "#4ade80", severityColor("info"))
	assert.Equal(t, "#4ade80", severityColor("unknown"))
}
