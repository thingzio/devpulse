package admin

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadReportConfig_Missing(t *testing.T) {
	// No env vars set — should return nil.
	cfg := loadReportConfig()
	assert.Nil(t, cfg)
}

func TestLoadReportConfig_Valid(t *testing.T) {
	t.Setenv("SENDGRID_API_KEY", "SG.test-key")
	t.Setenv("REPORT_TO_EMAIL", "to@example.com")
	t.Setenv("REPORT_FROM_EMAIL", "from@example.com")
	t.Setenv("REPORT_SUBJECT_PREFIX", "Test Report")

	cfg := loadReportConfig()
	require.NotNil(t, cfg)
	assert.Equal(t, "SG.test-key", cfg.SendGridAPIKey)
	assert.Equal(t, "to@example.com", cfg.ToEmail)
	assert.Equal(t, "from@example.com", cfg.FromEmail)
	assert.Equal(t, "Test Report", cfg.SubjectPrefix)
}

func TestLoadReportConfig_DefaultPrefix(t *testing.T) {
	t.Setenv("SENDGRID_API_KEY", "SG.key")
	t.Setenv("REPORT_TO_EMAIL", "to@example.com")
	t.Setenv("REPORT_FROM_EMAIL", "from@example.com")

	cfg := loadReportConfig()
	require.NotNil(t, cfg)
	assert.Equal(t, "DevPulse Daily Report", cfg.SubjectPrefix)
}

func testSummaryWithDeltas() summaryResponse {
	tenants := 5
	tenantsPct := 25.0
	repos := 2
	reposPct := 10.0
	events := int64(100)
	eventsPct := 50.0
	contribs := 3
	contribPct := 15.0
	installs := 1
	installPct := 0.0
	errors := 2
	errorsPct := 100.0

	d := &statsDelta{
		Tenants:         &tenants,
		TenantsPct:      &tenantsPct,
		Repos:           &repos,
		ReposPct:        &reposPct,
		Events:          &events,
		EventsPct:       &eventsPct,
		Contributors:    &contribs,
		ContribPct:      &contribPct,
		Installations:   &installs,
		InstallPct:      &installPct,
		ReposWithErrors: &errors,
		ErrorsPct:       &errorsPct,
	}

	return summaryResponse{
		Date: "2026-04-11",
		Current: platformStats{
			Tenants:           25,
			TenantsFree:       15,
			TenantsStarter:    5,
			TenantsPro:        3,
			TenantsEnterprise: 2,
			Repos:             42,
			Events:            12500,
			Contributors:      88,
			Installations:     5,
			ReposWithErrors:   4,
		},
		DoD: d,
		WoW: d,
		MoM: d,
		ErrorRepos: []errorRepo{
			{Org: "acme", Repo: "api", Errors: 7, LastError: "rate limited"},
		},
		UpdatedAt: "2026-04-11T10:00:00Z",
	}
}

func TestRenderReportHTML_WithDeltas(t *testing.T) {
	html := renderReportHTML(testSummaryWithDeltas(), "All systems operational.")

	assert.Contains(t, html, "Platform Summary")
	assert.Contains(t, html, "2026-04-11")
	assert.Contains(t, html, "25")                      // tenants current
	assert.Contains(t, html, "+5 (25.0%)")              // delta
	assert.Contains(t, html, "acme/api")                // error repo
	assert.Contains(t, html, "rate limited")            // last error
	assert.Contains(t, html, "All systems operational") // analysis
	assert.NotContains(t, html, "unavailable")
}

func TestRenderReportHTML_NilDeltas(t *testing.T) {
	summary := summaryResponse{
		Date: "2026-04-11",
		Current: platformStats{
			Tenants: 10,
			Repos:   5,
		},
	}

	html := renderReportHTML(summary, "Some analysis.")

	assert.Contains(t, html, "Platform Summary")
	assert.Contains(t, html, "\u2014") // em-dash for nil deltas
	assert.Contains(t, html, "10")     // tenants
	assert.NotContains(t, html, "Import Errors")
}

func TestRenderReportHTML_EmptyAnalysis(t *testing.T) {
	summary := summaryResponse{
		Date: "2026-04-11",
		Current: platformStats{
			Tenants: 1,
		},
	}

	html := renderReportHTML(summary, "")

	assert.Contains(t, html, "Metrics analysis unavailable")
}

func TestRenderReportText_NilDeltas(t *testing.T) {
	summary := summaryResponse{
		Date: "2026-04-11",
		Current: platformStats{
			Tenants:       10,
			Repos:         5,
			Events:        1000,
			Contributors:  20,
			Installations: 3,
		},
	}

	text := renderReportText(summary, "")

	assert.Contains(t, text, "PLATFORM SUMMARY")
	assert.Contains(t, text, "2026-04-11")
	assert.Contains(t, text, "\u2014")
	assert.Contains(t, text, "Metrics analysis unavailable")

	// Verify aligned columns exist
	lines := strings.Split(text, "\n")
	var headerFound bool
	for _, line := range lines {
		if strings.Contains(line, "Metric") && strings.Contains(line, "Current") {
			headerFound = true
			break
		}
	}
	assert.True(t, headerFound, "should have aligned header row")
}

func TestRenderReportText_WithErrorRepos(t *testing.T) {
	summary := testSummaryWithDeltas()
	text := renderReportText(summary, "Looks good.")

	assert.Contains(t, text, "REPOS WITH IMPORT ERRORS")
	assert.Contains(t, text, "acme/api")
	assert.Contains(t, text, "Looks good.")
}
