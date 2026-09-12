// Copyright 2026 Thingz LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
// SPDX-License-Identifier: Apache-2.0

package postgres

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSinceDate(t *testing.T) {
	t.Run("monthly range returns expected date", func(t *testing.T) {
		// 365 days > threshold → no Monday-snapping
		got := sinceDate(365)
		parsed, err := time.Parse("2006-01-02", got)
		require.NoError(t, err)
		expected := time.Now().UTC().AddDate(0, 0, -365)
		diff := expected.Sub(parsed)
		if diff < 0 {
			diff = -diff
		}
		assert.Less(t, diff.Hours(), 48.0)
	})

	t.Run("weekly range snaps to Monday", func(t *testing.T) {
		// 14 days ≤ threshold → snaps to previous Monday
		got := sinceDate(14)
		parsed, err := time.Parse("2006-01-02", got)
		require.NoError(t, err)
		assert.Equal(t, time.Monday, parsed.Weekday(),
			"sinceDate(14) should snap to Monday, got %s (%s)", got, parsed.Weekday())
	})

	t.Run("all weekly ranges snap to Monday", func(t *testing.T) {
		for _, days := range []int{14, 21, 28, 90, 180} {
			got := sinceDate(days)
			parsed, err := time.Parse("2006-01-02", got)
			require.NoError(t, err)
			assert.Equal(t, time.Monday, parsed.Weekday(),
				"sinceDate(%d) = %s should be Monday", days, got)
		}
	})
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

func TestAutoGranularity(t *testing.T) {
	tests := []struct {
		days int
		want Granularity
	}{
		{14, GranWeek},
		{28, GranWeek},
		{90, GranWeek},
		{180, GranWeek},
		{181, GranMonth},
		{365, GranMonth},
	}
	for _, tc := range tests {
		got := AutoGranularity(tc.days)
		assert.Equal(t, tc.want, got, "days=%d", tc.days)
	}
}

func TestGroupExpr(t *testing.T) {
	tests := []struct {
		gran Granularity
		col  string
		want string
	}{
		{GranMonth, "e.created_at", "TO_CHAR(e.created_at::date, 'YYYY-MM')"},
		{GranWeek, "e.created_at", "TO_CHAR(date_trunc('week', e.created_at::date), 'YYYY-MM-DD')"},
		{GranMonth, "e.date", "TO_CHAR(e.date::date, 'YYYY-MM')"},
		{GranWeek, "e.date", "TO_CHAR(date_trunc('week', e.date::date), 'YYYY-MM-DD')"},
	}
	for _, tc := range tests {
		got := GroupExpr(tc.gran, tc.col)
		assert.Equal(t, tc.want, got)
	}
}

func TestMomentumInterval(t *testing.T) {
	assert.Equal(t, "4 weeks", MomentumInterval(GranWeek))
	assert.Equal(t, "2 months", MomentumInterval(GranMonth))
}

func TestMomentumFormat(t *testing.T) {
	assert.Equal(t, "'YYYY-MM-DD'", MomentumFormat(GranWeek))
	assert.Equal(t, "'YYYY-MM'", MomentumFormat(GranMonth))
}

func TestTrendWindow(t *testing.T) {
	assert.Equal(t, 4, TrendWindow(GranWeek))
	assert.Equal(t, 3, TrendWindow(GranMonth))
}

func TestSinceDateWeeks(t *testing.T) {
	result := sinceDateWeeks(9)
	expected := time.Now().UTC().AddDate(0, 0, -63).Format("2006-01-02")
	assert.Equal(t, expected, result)
}

func TestGeneratePeriods_Weekly(t *testing.T) {
	periods := generatePeriods(28) // 4 weeks
	// Should produce 4-5 entries (4 full weeks + possible current partial)
	assert.GreaterOrEqual(t, len(periods), 4)
	assert.LessOrEqual(t, len(periods), 6)
	for _, p := range periods {
		assert.Len(t, p, 10, "weekly period should be YYYY-MM-DD: %s", p)
		parsed, err := time.Parse("2006-01-02", p)
		require.NoError(t, err)
		assert.Equal(t, time.Monday, parsed.Weekday(), "period %s should be Monday", p)
	}
}

func TestGeneratePeriods_Monthly(t *testing.T) {
	periods := generatePeriods(365) // 12 months
	assert.GreaterOrEqual(t, len(periods), 12)
	assert.LessOrEqual(t, len(periods), 14)
	for _, p := range periods {
		assert.Len(t, p, 7, "monthly period should be YYYY-MM: %s", p)
	}
}

func TestGapFiller(t *testing.T) {
	// Use dynamically generated periods so the test doesn't drift with time.
	periods := generatePeriods(28)
	require.GreaterOrEqual(t, len(periods), 2, "need at least 2 periods")
	labels := []string{periods[0], periods[len(periods)-1]}

	gf := newGapFiller(28, labels)

	intData := gf.fillInt([]int{10, 30})
	found := 0
	for _, v := range intData {
		if v > 0 {
			found++
		}
	}
	assert.Equal(t, 2, found, "should have exactly 2 non-zero values")
	assert.Equal(t, len(gf.periods), len(intData), "output length should match periods")

	floatData := gf.fillFloat64([]float64{1.5, 3.5})
	assert.Equal(t, len(gf.periods), len(floatData))
}

func TestGapFillSlice(t *testing.T) {
	periods := generatePeriods(28)
	require.GreaterOrEqual(t, len(periods), 2, "need at least 2 periods")
	labels := []string{periods[0], periods[len(periods)-1]}

	gf := newGapFiller(28, labels)

	intResult := gapFillSlice(gf, []int{5, 15})
	assert.Equal(t, len(gf.periods), len(intResult))

	floatResult := gapFillSlice(gf, []float64{2.5, 7.5})
	assert.Equal(t, len(gf.periods), len(floatResult))
}
