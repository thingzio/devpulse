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

package server

import "github.com/thingzio/devpulse/pkg/platformstats"

// platformStats is an alias so existing call sites and tests stay short.
// The canonical type lives in pkg/platformstats — both the admin handler
// and the import job snapshot through that package.
type platformStats = platformstats.Stats

type tenantSummary struct {
	Username         string `json:"username"`
	Email            string `json:"email"`
	Name             string `json:"name"`
	Plan             string `json:"plan"`
	Status           string `json:"status"`
	MaxRepos         int    `json:"max_repos"`
	MaxEventsPerWeek int    `json:"max_events_per_week"`
	CreatedAt        string `json:"created_at"`
	LastSignIn       string `json:"last_sign_in"`
}

type tenantDetail struct {
	Username         string       `json:"username"`
	Email            string       `json:"email"`
	Name             string       `json:"name"`
	Company          string       `json:"company"`
	Location         string       `json:"location"`
	Bio              string       `json:"bio"`
	Plan             string       `json:"plan"`
	Status           string       `json:"status"`
	MaxRepos         int          `json:"max_repos"`
	MaxEventsPerWeek int          `json:"max_events_per_week"`
	CreatedAt        string       `json:"created_at"`
	LastSignIn       string       `json:"last_sign_in"`
	DigestLastSentAt string       `json:"digest_last_sent_at"`
	Repos            []repoDetail `json:"repos"`
}

type repoDetail struct {
	Name           string  `json:"name"`
	Events         int     `json:"events"`
	WeeklyEvents   int     `json:"weekly_events"`
	WeeklyPct      float64 `json:"weekly_pct"`
	LastImport     string  `json:"last_import"`
	PRTotal        int     `json:"pr_total"`
	PRMissingSize  int     `json:"pr_missing_size"`
	Contributors   int     `json:"contributors"`
	Scored         int     `json:"scored"`
	BackfillDays   int     `json:"backfill_days"`
	BackfillTarget int     `json:"backfill_target"`
}

type tokenStatus struct {
	Login          string `json:"login"`
	InstallationID int64  `json:"installation_id"`
	Limit          int    `json:"limit"`
	Used           int    `json:"used"`
	Remaining      int    `json:"remaining"`
	ResetAt        string `json:"reset_at,omitempty"`
	Error          string `json:"error,omitempty"`
}

type statsDelta struct {
	Tenants         *int     `json:"tenants,omitempty"`
	TenantsPct      *float64 `json:"tenants_pct,omitempty"`
	Repos           *int     `json:"repos,omitempty"`
	ReposPct        *float64 `json:"repos_pct,omitempty"`
	Events          *int64   `json:"events,omitempty"`
	EventsPct       *float64 `json:"events_pct,omitempty"`
	Contributors    *int     `json:"contributors,omitempty"`
	ContribPct      *float64 `json:"contributors_pct,omitempty"`
	Installations   *int     `json:"installations,omitempty"`
	InstallPct      *float64 `json:"installations_pct,omitempty"`
	ReposWithErrors *int     `json:"repos_with_errors,omitempty"`
	ErrorsPct       *float64 `json:"repos_with_errors_pct,omitempty"`
}

type errorRepo struct {
	Org       string `json:"org"`
	Repo      string `json:"repo"`
	Errors    int    `json:"errors"`
	LastError string `json:"last_error"`
}

// stuckInsightsRepo flags a repo whose insights should have regenerated under
// the importer's staleness gates (age > 7 days, event delta > 10%) but haven't —
// either because insights are missing entirely or the last regeneration was
// over two weeks ago despite enough new activity to qualify.
type stuckInsightsRepo struct {
	Org           string  `json:"org"`
	Repo          string  `json:"repo"`
	GeneratedAt   string  `json:"generated_at"`
	AgeDays       float64 `json:"age_days"`
	SavedEvents   int     `json:"saved_events"`
	CurrentEvents int     `json:"current_events"`
	DeltaPct      float64 `json:"delta_pct"`
	HasNoInsights bool    `json:"has_no_insights"`
}

type opsMetrics struct {
	ActiveTenants7d    int `json:"active_tenants_7d"`
	Onboarded          int `json:"onboarded"`
	Suspended          int `json:"suspended"`
	ReposWithInsights  int `json:"repos_with_insights"`
	ScoredContributors int `json:"scored_contributors"`
}

type summaryResponse struct {
	Date               string              `json:"date"`
	Current            platformStats       `json:"current"`
	Ops                opsMetrics          `json:"ops"`
	DoD                *statsDelta         `json:"dod,omitempty"`
	WoW                *statsDelta         `json:"wow,omitempty"`
	MoM                *statsDelta         `json:"mom,omitempty"`
	ErrorRepos         []errorRepo         `json:"error_repos"`
	StuckInsightsRepos []stuckInsightsRepo `json:"stuck_insights_repos"`
	UpdatedAt          string              `json:"updated_at"`
}
