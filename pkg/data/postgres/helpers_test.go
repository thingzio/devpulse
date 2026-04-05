package postgres

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSinceDate(t *testing.T) {
	tests := []struct {
		months int
	}{
		{1}, {3}, {6}, {12},
	}
	for _, tc := range tests {
		got := sinceDate(tc.months)
		_, err := time.Parse("2006-01-02", got)
		require.NoError(t, err, "sinceDate(%d) returned non-date %q", tc.months, got)

		expected := time.Now().UTC().AddDate(0, -tc.months, 0)
		// Allow ±1 day tolerance for clock skew at midnight.
		parsed, _ := time.Parse("2006-01-02", got)
		diff := expected.Sub(parsed)
		if diff < 0 {
			diff = -diff
		}
		assert.Less(t, diff.Hours(), 48.0, "sinceDate(%d) too far from expected", tc.months)
	}
}

func TestCleanEntityName(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		// Noise-word stripping
		{name: "strips INC", input: "ACME INC", want: "ACME"},
		{name: "strips LLC", input: "Widgets LLC", want: "WIDGETS"},
		{name: "strips CORP", input: "BigCorp CORP", want: "BIGCORP"},
		{name: "strips LTD", input: "Gadgets LTD", want: "GADGETS"},
		{name: "strips CO", input: "Acme CO", want: "ACME"},
		{name: "strips GMBH", input: "Deutschland GMBH", want: "DEUTSCHLAND"},
		{name: "strips GROUP", input: "Tech GROUP", want: "TECH"},
		{name: "strips PVT", input: "India PVT", want: "INDIA"},

		// Pre-substitutions (exact match before regex)
		{name: "GCP → GOOGLE", input: "GCP", want: "GOOGLE"},
		{name: "GOOGLECLOUD → GOOGLE", input: "GOOGLECLOUD", want: "GOOGLE"},
		{name: "GOOGLECLOUDPLATFORM → GOOGLE", input: "GOOGLECLOUDPLATFORM", want: "GOOGLE"},
		{name: "HUAWEICLOUD → HUAWEI", input: "HUAWEICLOUD", want: "HUAWEI"},
		{name: "REDHATOFFICIAL → RED HAT", input: "REDHATOFFICIAL", want: "RED HAT"},
		{name: "CHAINGUARDDEV → CHAINGUARD", input: "CHAINGUARDDEV", want: "CHAINGUARD"},
		{name: "IBM RESEARCH → IBM", input: "IBM RESEARCH", want: "IBM"},
		{name: "IBM CODAITY → IBM", input: "IBM CODAITY", want: "IBM"},
		{name: "LINE PLUS → LINE", input: "LINE PLUS", want: "LINE"},
		{name: "MICROSOFT CHINA → MICROSOFT", input: "MICROSOFT CHINA", want: "MICROSOFT"},

		// S&P: & stripped by regex → "SP GLOBAL INC"; noise stripped → "SP GLOBAL"; sub → "SP GLOBAL".
		{name: "S&P GLOBAL INC", input: "S&P GLOBAL INC", want: "SP GLOBAL"},

		// New substitutions: Amazon/AWS
		{name: "AWS → AMAZON", input: "AWS", want: "AMAZON"},
		{name: "Amazon Web Services Inc → AMAZON", input: "Amazon Web Services Inc", want: "AMAZON"},
		{name: "amzn → AMAZON", input: "amzn", want: "AMAZON"},

		// New substitutions: Meta/Facebook
		{name: "Facebook → META", input: "Facebook", want: "META"},
		{name: "Meta Platforms → META", input: "Meta Platforms", want: "META"},
		{name: "Oculus → META", input: "Oculus", want: "META"},

		// New substitutions: Microsoft subsidiaries
		{name: "GitHub → MICROSOFT", input: "GitHub", want: "MICROSOFT"},
		{name: "Xamarin → MICROSOFT", input: "Xamarin", want: "MICROSOFT"},

		// New substitutions: Red Hat
		{name: "Red Hat → RED HAT", input: "Red Hat", want: "RED HAT"},
		{name: "redhat → RED HAT", input: "redhat", want: "RED HAT"},
		{name: "CoreOS → RED HAT", input: "CoreOS", want: "RED HAT"},

		// New substitutions: VMware acquisitions
		{name: "Pivotal → VMWARE", input: "Pivotal", want: "VMWARE"},
		{name: "Heptio → VMWARE", input: "Heptio", want: "VMWARE"},
		{name: "Bitnami → VMWARE", input: "Bitnami", want: "VMWARE"},

		// New substitutions: NVIDIA
		{name: "NVIDIA Corporation → NVIDIA", input: "NVIDIA Corporation", want: "NVIDIA"},

		// New substitutions: other
		{name: "Sendgrid → TWILIO", input: "Sendgrid", want: "TWILIO"},
		{name: "Rancher Labs → SUSE", input: "Rancher Labs", want: "SUSE"},
		{name: "Elastic NV → ELASTIC", input: "Elastic NV", want: "ELASTIC"},
		{name: "Grafana Labs → GRAFANA", input: "Grafana Labs", want: "GRAFANA"},
		{name: "SAP SE → SAP", input: "SAP SE", want: "SAP"},
		{name: "JPMorgan Chase → JPMORGAN CHASE", input: "JPMorgan Chase", want: "JPMORGAN CHASE"},
		{name: "Puppet Labs → PUPPET", input: "Puppet Labs", want: "PUPPET"},
		{name: "Magento → ADOBE", input: "Magento", want: "ADOBE"},

		// Regex strips non-alphanumeric (except spaces)
		{name: "strips punctuation", input: "Tech, Inc.", want: "TECH"},
		{name: "strips dots in noise word", input: "Firm P.C.", want: "FIRM"},

		// Whitespace handling
		{name: "trims leading/trailing spaces", input: "  Google  ", want: "GOOGLE"},
		{name: "multi-word preserved", input: "Red Hat", want: "RED HAT"},

		// Empty / whitespace only
		{name: "empty string stays empty", input: "", want: ""},
		{name: "whitespace only stays empty", input: "   ", want: ""},
		{name: "all noise stays empty", input: "INC LLC", want: ""},

		// Case normalisation
		{name: "lowercase input uppercased", input: "apple", want: "APPLE"},
		{name: "mixed case uppercased", input: "JetBrains", want: "JETBRAINS"},

		// Long multi-word substitution
		{
			name:  "INTERNATIONAL BUSINESS MACHINES CORPORATION → IBM",
			input: "International Business Machines Corporation",
			want:  "IBM",
		},
		{
			name:  "VERVERICA long name → VERVERICA",
			input: "VERVERICA ORIGINAL CREATORS OF APACHE FLINK",
			want:  "VERVERICA",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := cleanEntityName(tc.input)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestBuildDailyTotalsExtra(t *testing.T) {
	t.Run("no activity returns descending current counts", func(t *testing.T) {
		result := buildDailyTotals(100, 50, nil, nil, 3)
		require.Len(t, result, 4) // days+1
		// All entries should have the same stars/forks since there's no delta
		for _, r := range result {
			assert.Equal(t, 100, r.Stars)
			assert.Equal(t, 50, r.Forks)
		}
	})

	t.Run("stars delta subtracted going back in time", func(t *testing.T) {
		// 1 star on today's date
		today := time.Now().UTC().Format("2006-01-02")
		stars := map[string]int{today: 5}
		result := buildDailyTotals(10, 0, stars, nil, 2)
		require.Len(t, result, 3)
		// Last entry (today) = 10
		assert.Equal(t, 10, result[len(result)-1].Stars)
		// Second-to-last (yesterday) = 10 - 5 = 5
		assert.Equal(t, 5, result[len(result)-2].Stars)
	})

	t.Run("forks delta subtracted going back in time", func(t *testing.T) {
		today := time.Now().UTC().Format("2006-01-02")
		forks := map[string]int{today: 3}
		result := buildDailyTotals(0, 20, nil, forks, 2)
		require.Len(t, result, 3)
		assert.Equal(t, 20, result[len(result)-1].Forks)
		assert.Equal(t, 17, result[len(result)-2].Forks)
	})

	t.Run("stars never go below zero", func(t *testing.T) {
		today := time.Now().UTC().Format("2006-01-02")
		stars := map[string]int{today: 1000}
		result := buildDailyTotals(5, 0, stars, nil, 2)
		for _, r := range result {
			assert.GreaterOrEqual(t, r.Stars, 0)
		}
	})

	t.Run("dates are in ascending order", func(t *testing.T) {
		result := buildDailyTotals(10, 5, nil, nil, 5)
		require.Len(t, result, 6)
		for i := 1; i < len(result); i++ {
			assert.True(t, result[i].Date >= result[i-1].Date,
				"dates out of order: %s before %s", result[i-1].Date, result[i].Date)
		}
	})

	t.Run("zero days returns single entry", func(t *testing.T) {
		result := buildDailyTotals(7, 3, nil, nil, 0)
		require.Len(t, result, 1)
		assert.Equal(t, 7, result[0].Stars)
		assert.Equal(t, 3, result[0].Forks)
	})

	t.Run("date format is YYYY-MM-DD", func(t *testing.T) {
		result := buildDailyTotals(1, 1, nil, nil, 1)
		for _, r := range result {
			parts := strings.Split(r.Date, "-")
			assert.Len(t, parts, 3)
		}
	})
}
