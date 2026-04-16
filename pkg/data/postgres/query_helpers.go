package postgres

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/thingzio/devpulse/pkg/data"
)

const (
	nonAlphaNumRegex string = "[^a-zA-Z0-9 ]+"

	// botExcludeSQL filters out bot accounts using the "e" table alias.
	botExcludeSQL = `AND e.username NOT LIKE '%[bot]'
		AND LOWER(e.username) NOT IN (` + data.BotNames + `)`

	// botExcludeDSQL filters out bot accounts using the "d" table alias.
	botExcludeDSQL = `AND d.username NOT LIKE '%[bot]'
		AND LOWER(d.username) NOT IN (` + data.BotNames + `)`

	// botExcludeTpl is botExcludeSQL with % escaped for use in fmt.Sprintf templates.
	botExcludeTpl = `AND e.username NOT LIKE '%%[bot]'
		AND LOWER(e.username) NOT IN (` + data.BotNames + `)`

	// botExcludeDTpl is botExcludeDSQL with % escaped for use in fmt.Sprintf templates.
	botExcludeDTpl = `AND d.username NOT LIKE '%%[bot]'
		AND LOWER(d.username) NOT IN (` + data.BotNames + `)`

	// botExcludePrTpl is botExcludePrSQL with % escaped for fmt.Sprintf templates.
	botExcludePrTpl = `AND pr.username NOT LIKE '%%[bot]'
		AND LOWER(pr.username) NOT IN (` + data.BotNames + `)`

	// forkExcludeSQL excludes fork events from the join so only code/comment
	// activity (PR, PR review, issue, issue comment) counts toward reputation.
	forkExcludeSQL = `AND e.type != 'fork'`
)

var (
	entityRegEx = regexp.MustCompile(nonAlphaNumRegex)

	entityNoise = map[string]bool{
		"B.V.":        true,
		"CDL":         true,
		"CO":          true,
		"COMPANY":     true,
		"CORP":        true,
		"CORPORATION": true,
		"GMBH":        true,
		"GROUP":       true,
		"INC":         true,
		"LLC":         true,
		"LC":          true,
		"P.C.":        true,
		"P.A.":        true,
		"S.C.":        true,
		"LTD.":        true,
		"CHTD.":       true,
		"PC":          true,
		"LTD":         true,
		"PVT":         true,
		"SE":          true,
		"S.A.":        true,
	}

	entitySubstitutions = map[string]string{
		// ---- Original mappings ----
		"CHAINGUARDDEV": "CHAINGUARD",
		"INTERNATIONAL INSTITUTE OF INFORMATION TECHNOLOGY BANGALORE": "IIIT BANGALORE",
		"LINE PLUS": "LINE",
		"SP GLOBAL": "SP GLOBAL",
		"VERVERICA ORIGINAL CREATORS OF APACHE FLINK": "VERVERICA",

		// ---- Adobe (acquired brands) ----
		"AVIARY":  "ADOBE",
		"BEHANCE": "ADOBE",
		"FOTOLIA": "ADOBE",
		"MAGENTO": "ADOBE",

		// ---- Amazon / AWS ----
		"AMAZON WEB SERVICES": "AMAZON",
		"AMAZONCOM":           "AMAZON",
		"AMZN":                "AMAZON",
		"AWS":                 "AMAZON",

		// ---- Cisco ----
		"APPDYNAMICS":   "CISCO",
		"CISCO SYSTEMS": "CISCO",
		"DUO SECURITY":  "CISCO",
		"EPSAGON":       "CISCO",

		// ---- Cloudera ----
		"HORTONWORKS": "CLOUDERA",

		// ---- Equinix ----
		"EQUINIX METAL": "EQUINIX",
		"PACKET HOST":   "EQUINIX",

		// ---- Google ----
		"GCP":                 "GOOGLE",
		"GOOGLE CLOUD":        "GOOGLE",
		"GOOGLECLOUD":         "GOOGLE",
		"GOOGLECLOUDPLATFORM": "GOOGLE",

		// ---- Huawei ----
		"FUTUREWEI":              "HUAWEI",
		"FUTUREWEI TECHNOLOGIES": "HUAWEI",
		"HUAWEI TECHNOLOGIES":    "HUAWEI",
		"HUAWEICLOUD":            "HUAWEI",

		// ---- IBM ----
		"IBM CODAITY":                                 "IBM",
		"IBM RESEARCH":                                "IBM",
		"INTERNATIONAL BUSINESS MACHINES":             "IBM",
		"INTERNATIONAL BUSINESS MACHINES CORPORATION": "IBM",

		// ---- Meta (Facebook rebrand) ----
		"FACEBOOK":       "META",
		"META PLATFORMS": "META",
		"OCULUS":         "META",

		// ---- Microsoft ----
		"AZURE":                 "MICROSOFT",
		"AZURE CLOUD":           "MICROSOFT",
		"GITHUB":                "MICROSOFT",
		"MICROSOFT CHINA":       "MICROSOFT",
		"MICROSOFT CORPORATION": "MICROSOFT",
		"XAMARIN":               "MICROSOFT",

		// ---- NVIDIA ----
		"NVIDIA CORPORATION": "NVIDIA",

		// ---- Oracle ----
		"OCI":                         "ORACLE",
		"ORACLE AMERICA":              "ORACLE",
		"ORACLE CLOUD INFRASTRUCTURE": "ORACLE",
		"ORACLE OCI":                  "ORACLE",
		"WERCKER":                     "ORACLE",

		// ---- Red Hat ----
		"COREOS":         "RED HAT",
		"REDHAT":         "RED HAT",
		"REDHATOFFICIAL": "RED HAT",

		// ---- Salesforce ----
		"SALESFORCECOM": "SALESFORCE",

		// ---- SUSE ----
		"RANCHER LABS": "SUSE",

		// ---- Twilio ----
		"SENDGRID": "TWILIO",

		// ---- VMware ----
		"BITNAMI": "VMWARE",
		"HEPTIO":  "VMWARE",
		"PIVOTAL": "VMWARE",

		// ---- Other well-known tech companies (canonical forms) ----
		"ALIBABA GROUP":            "ALIBABA",
		"ATLASSIAN PTY":            "ATLASSIAN",
		"BOOKING":                  "BOOKING.COM",
		"BOOKING COM":              "BOOKING.COM",
		"CAPITAL ONE":              "CAPITAL ONE",
		"DATADOG":                  "DATADOG",
		"ELASTIC NV":               "ELASTIC",
		"ELASTIC SEARCH":           "ELASTIC",
		"ELASTICSEARCH":            "ELASTIC",
		"EMBL EBI":                 "EMBL-EBI",
		"EPAM SYSTEMS":             "EPAM",
		"GRAFANA LABS":             "GRAFANA",
		"JETBRAINS SRO":            "JETBRAINS",
		"JPMORGAN":                 "JPMORGAN CHASE",
		"JPMORGAN CHASE":           "JPMORGAN CHASE",
		"JP MORGAN":                "JPMORGAN CHASE",
		"JPMORGAN CHASE CO":        "JPMORGAN CHASE",
		"MASTERCARD INTERNATIONAL": "MASTERCARD",
		"MONGODB":                  "MONGODB",
		"NETEASE":                  "NETEASE",
		"PALANTIR TECHNOLOGIES":    "PALANTIR",
		"PUPPET LABS":              "PUPPET",
		"SAP SE":                   "SAP",
		"SQUARE":                   "BLOCK",
		"THE GUARDIAN":             "THE GUARDIAN",
		"TRAVIS CI":                "TRAVIS CI",
		"UNITY TECHNOLOGIES":       "UNITY",
		"WIKIMEDIA FOUNDATION":     "WIKIMEDIA",
	}
)

func sinceDate(days int) string {
	now := time.Now().UTC()
	since := now.AddDate(0, 0, -days)
	// For weekly granularity windows, snap to the previous Monday so
	// SQL date_trunc('week', ...) produces clean, full-week buckets.
	if days <= autoGranularityThreshold {
		wd := since.Weekday()
		offset := int(wd) - 1 // Monday=0 ... Saturday=5
		if wd == time.Sunday {
			offset = 6
		}
		since = since.AddDate(0, 0, -offset)
	}
	return since.Format("2006-01-02")
}

// Granularity controls how time-series data is bucketed.
type Granularity string

const (
	GranWeek  Granularity = "week"
	GranMonth Granularity = "month"

	// autoGranularityThreshold: time ranges up to this many days use weekly grouping.
	autoGranularityThreshold = 180
)

// AutoGranularity selects weekly or monthly grouping based on the time window.
func AutoGranularity(days int) Granularity {
	if days <= autoGranularityThreshold {
		return GranWeek
	}
	return GranMonth
}

// GroupExpr returns the SQL expression that buckets a column by granularity.
//   - GranMonth: "SUBSTRING(col, 1, 7)"          → "2024-03"
//   - GranWeek:  "TO_CHAR(date_trunc(...))"       → "2024-03-04" (Monday)
func GroupExpr(g Granularity, col string) string {
	if g == GranWeek {
		return fmt.Sprintf("TO_CHAR(date_trunc('week', %s::date), 'YYYY-MM-DD')", col)
	}
	return fmt.Sprintf("SUBSTRING(%s, 1, 7)", col)
}

// MomentumInterval returns the SQL interval for the rolling momentum window.
func MomentumInterval(g Granularity) string {
	if g == GranWeek {
		return "4 weeks"
	}
	return "2 months"
}

// MomentumFormat returns the TO_CHAR format for momentum date comparison.
func MomentumFormat(g Granularity) string {
	if g == GranWeek {
		return "'YYYY-MM-DD'"
	}
	return "'YYYY-MM'"
}

// TrendWindow returns the number of data points for the moving average.
func TrendWindow(g Granularity) int {
	if g == GranWeek {
		return 4
	}
	return 3
}

func sinceDateWeeks(weeks int) string {
	return time.Now().UTC().AddDate(0, 0, -weeks*7).Format("2006-01-02")
}

// gapFiller pads time-series results so every expected period is present.
type gapFiller struct {
	periods []string
	index   map[string]int
}

// newGapFiller builds a filler from the query's days parameter and the
// labels actually returned by the database.
func newGapFiller(days int, labels []string) *gapFiller {
	periods := generatePeriods(days)
	idx := make(map[string]int, len(labels))
	for i, l := range labels {
		idx[l] = i
	}
	return &gapFiller{periods: periods, index: idx}
}

func (g *gapFiller) fillInt(orig []int) []int {
	out := make([]int, len(g.periods))
	for i, p := range g.periods {
		if idx, ok := g.index[p]; ok && idx < len(orig) {
			out[i] = orig[idx]
		}
	}
	return out
}

func (g *gapFiller) fillFloat64(orig []float64) []float64 {
	out := make([]float64, len(g.periods))
	for i, p := range g.periods {
		if idx, ok := g.index[p]; ok && idx < len(orig) {
			out[i] = orig[idx]
		}
	}
	return out
}

func gapFillSlice[T int | float64](gf *gapFiller, orig []T) []T {
	out := make([]T, len(gf.periods))
	for i, p := range gf.periods {
		if idx, ok := gf.index[p]; ok && idx < len(orig) {
			out[i] = orig[idx]
		}
	}
	return out
}

// generatePeriods returns every period label from sinceDate(days) to today.
func generatePeriods(days int) []string {
	gran := AutoGranularity(days)
	since := sinceDate(days)
	start, _ := time.Parse("2006-01-02", since)
	now := time.Now().UTC()

	var periods []string
	if gran == GranWeek {
		for d := start; !d.After(now); d = d.AddDate(0, 0, 7) {
			periods = append(periods, d.Format("2006-01-02"))
		}
	} else {
		d := time.Date(start.Year(), start.Month(), 1, 0, 0, 0, 0, time.UTC)
		nowMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
		for !d.After(nowMonth) {
			periods = append(periods, d.Format("2006-01"))
			d = d.AddDate(0, 1, 0)
		}
	}
	return periods
}

func rollbackTransaction(tx *sql.Tx) {
	if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
		slog.Error("error rolling back transaction", "error", err)
	}
}

// queryBuilder constructs optional WHERE clauses for non-nil parameters.
// This replaces the COALESCE($1, e.col) anti-pattern which prevents
// the planner from using indexes.
type queryBuilder struct {
	paramIdx int
	clauses  []string
	args     []any
}

func newQueryBuilder(startParam int) *queryBuilder {
	return &queryBuilder{paramIdx: startParam}
}

// addOptional appends "col = $N" only when val is non-nil.
func (qb *queryBuilder) addOptional(col string, val *string) {
	if val == nil {
		return
	}
	qb.clauses = append(qb.clauses, fmt.Sprintf("%s = $%d", col, qb.paramIdx))
	qb.args = append(qb.args, *val)
	qb.paramIdx++
}

// addEntityFilter appends "col = $N" for non-nil entity.
// Replaces the COALESCE(d.entity, ”) = COALESCE($N, COALESCE(d.entity, ”)) pattern.
func (qb *queryBuilder) addEntityFilter(col string, val *string) {
	if val == nil {
		return
	}
	qb.clauses = append(qb.clauses, fmt.Sprintf("%s = $%d", col, qb.paramIdx))
	qb.args = append(qb.args, *val)
	qb.paramIdx++
}

// whereClause returns all clauses as " AND col = $N AND col2 = $M" or "" if empty.
func (qb *queryBuilder) whereClause() string {
	if len(qb.clauses) == 0 {
		return ""
	}
	var sb strings.Builder
	for _, c := range qb.clauses {
		sb.WriteString(" AND ")
		sb.WriteString(c)
	}
	return sb.String()
}

// nextParam returns the next available parameter index.
func (qb *queryBuilder) nextParam() int {
	return qb.paramIdx
}
