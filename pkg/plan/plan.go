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

package plan

import "fmt"

const (
	Free       = "free"
	Starter    = "starter"
	Pro        = "pro"
	Enterprise = "enterprise"
)

// Plan holds both logic fields (used by backend) and display fields (used by UI).
type Plan struct {
	// Logic fields — used by backend code for limits/gating.
	Name               string
	MaxRepos           int
	MaxEventsPerWeek   int
	MaxDataRangeMonths int
	AILevel            int
	PDFExport          bool
	CSVExport          bool

	// Display fields — used only by the plans comparison table.
	DisplayName       string
	PriceLabel        string
	ReposLabel        string
	PrivateReposLabel string
	EventsLabel       string
	RetentionLabel    string
	PDFExportLabel    string
	CSVExportLabel    string
	AILabel           string
	ReputationLabel   string
	APILabel          string
	ImportLabel       string
	DedicatedLabel    string
}

// Feature represents one row in the plans comparison table.
type Feature struct {
	ID     string   // HTML anchor id
	Label  string   // first column text
	Values []string // one value per plan, in display order
	Span   bool     // if true, single value spans all columns
}

// Limits is an alias for backward-compatible use in admin handlers.
type Limits = Plan

// planOrder controls display order in the comparison table.
var planOrder = []string{Free, Starter, Pro, Enterprise}

var plans = map[string]Plan{
	Free: {
		Name:               Free,
		MaxRepos:           1,
		MaxEventsPerWeek:   500,
		MaxDataRangeMonths: 3,
		AILevel:            0,
		PDFExport:          false,
		CSVExport:          false,

		DisplayName:       "Free",
		PriceLabel:        "",
		ReposLabel:        "1",
		PrivateReposLabel: "\u2014",
		EventsLabel:       "500",
		RetentionLabel:    "3 months",
		PDFExportLabel:    "\u2014",
		CSVExportLabel:    "\u2014",
		AILabel:           "\u2014",
		ReputationLabel:   "Shallow",
		APILabel:          "\u2014",
		ImportLabel:       "Daily",
		DedicatedLabel:    "\u2014",
	},
	Starter: {
		Name:               Starter,
		MaxRepos:           5,
		MaxEventsPerWeek:   2500,
		MaxDataRangeMonths: 6,
		AILevel:            1,
		PDFExport:          true,
		CSVExport:          false,

		DisplayName:       "Starter",
		PriceLabel:        "$0/mo*",
		ReposLabel:        "5",
		PrivateReposLabel: "\u2014",
		EventsLabel:       "2,500",
		RetentionLabel:    "6 months",
		PDFExportLabel:    "Yes",
		CSVExportLabel:    "\u2014",
		AILabel:           "Insights",
		ReputationLabel:   "Shallow",
		APILabel:          "\u2014",
		ImportLabel:       "Hourly",
		DedicatedLabel:    "\u2014",
	},
	Pro: {
		Name:               Pro,
		MaxRepos:           25,
		MaxEventsPerWeek:   15000,
		MaxDataRangeMonths: 12,
		AILevel:            2,
		PDFExport:          true,
		CSVExport:          true,

		DisplayName:       "Pro",
		PriceLabel:        "$0/mo*",
		ReposLabel:        "25",
		PrivateReposLabel: "\u2014",
		EventsLabel:       "15,000",
		RetentionLabel:    "12 months",
		PDFExportLabel:    "Yes",
		CSVExportLabel:    "Yes",
		AILabel:           "Insights + Actions",
		ReputationLabel:   "Deep",
		APILabel:          "\u2014",
		ImportLabel:       "Hourly",
		DedicatedLabel:    "\u2014",
	},
	Enterprise: {
		Name:               Enterprise,
		MaxRepos:           0,
		MaxEventsPerWeek:   0,
		MaxDataRangeMonths: 0,
		AILevel:            2,
		PDFExport:          true,
		CSVExport:          true,

		DisplayName:       "Enterprise",
		PriceLabel:        "$0/mo*",
		ReposLabel:        "Unlimited",
		PrivateReposLabel: "Yes",
		EventsLabel:       "Unlimited",
		RetentionLabel:    "Unlimited",
		PDFExportLabel:    "Yes",
		CSVExportLabel:    "Yes",
		AILabel:           "Insights + Actions",
		ReputationLabel:   "Deep",
		APILabel:          "Yes",
		ImportLabel:       "Hourly + On-demand",
		DedicatedLabel:    "Optional (quoted)",
	},
}

// All exposes plans as a map for backward-compatible iteration (admin dropdowns).
var All = plans

// Get returns the plan for a name and whether it exists.
func Get(name string) (Plan, bool) {
	p, ok := plans[name]
	return p, ok
}

// FreeLimits returns the free plan.
func FreeLimits() Plan {
	return plans[Free]
}

// DisplayPlans returns plans in display order for the comparison table.
func DisplayPlans() []Plan {
	out := make([]Plan, len(planOrder))
	for i, name := range planOrder {
		out[i] = plans[name]
	}
	return out
}

// DisplayFeatures returns the feature comparison rows for the plans table.
func DisplayFeatures() []Feature {
	dp := DisplayPlans()
	return []Feature{
		{ID: "feature-repos", Label: "Repos", Values: pluck(dp, func(p Plan) string { return p.ReposLabel })},
		{ID: "feature-private-repos", Label: "Private Repos", Values: pluck(dp, func(p Plan) string { return p.PrivateReposLabel })},
		{ID: "feature-events", Label: "Events/Week", Values: pluck(dp, func(p Plan) string { return p.EventsLabel })},
		{ID: "feature-retention", Label: "Data Retention", Values: pluck(dp, func(p Plan) string { return p.RetentionLabel })},
		{ID: "feature-export-pdf", Label: "Data Export (PDF)", Values: pluck(dp, func(p Plan) string { return p.PDFExportLabel })},
		{ID: "feature-export-csv", Label: "Data Export (CSV/ZIP)", Values: pluck(dp, func(p Plan) string { return p.CSVExportLabel })},
		{ID: "feature-ai", Label: "AI", Values: pluck(dp, func(p Plan) string { return p.AILabel })},
		{ID: "feature-reputation", Label: "Reputation Score", Values: pluck(dp, func(p Plan) string { return p.ReputationLabel })},
		{ID: "feature-api", Label: "API Access", Values: pluck(dp, func(p Plan) string { return p.APILabel })},
		{ID: "feature-import", Label: "Import Frequency", Values: pluck(dp, func(p Plan) string { return p.ImportLabel })},
		{ID: "feature-dedicated", Label: "Dedicated Instance", Values: pluck(dp, func(p Plan) string { return p.DedicatedLabel })},
	}
}

func pluck(ps []Plan, fn func(Plan) string) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = fn(p)
	}
	return out
}

const unlimitedLabel = "unlimited"

// FormatMaxRepos returns a human-readable string for max repos (0 = unlimited).
func (p Plan) FormatMaxRepos() string {
	if p.MaxRepos == 0 {
		return unlimitedLabel
	}
	return fmt.Sprintf("%d", p.MaxRepos)
}

// FormatMaxEvents returns a human-readable string for max events/week (0 = unlimited).
func (p Plan) FormatMaxEvents() string {
	if p.MaxEventsPerWeek == 0 {
		return unlimitedLabel
	}
	return fmt.Sprintf("%d", p.MaxEventsPerWeek)
}
