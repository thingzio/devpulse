package postgres

import (
	"database/sql"
	"log/slog"
	"regexp"
	"time"
)

const (
	nonAlphaNumRegex string = "[^a-zA-Z0-9 ]+"

	// botExcludeSQL filters out bot accounts using the "e" table alias.
	botExcludeSQL = `AND e.username NOT LIKE '%[bot]'
		AND LOWER(e.username) NOT IN ('copilot','github-copilot','claude','anthropic-claude')`

	// botExcludeDSQL filters out bot accounts using the "d" table alias.
	botExcludeDSQL = `AND d.username NOT LIKE '%[bot]'
		AND LOWER(d.username) NOT IN ('copilot','github-copilot','claude','anthropic-claude')`

	// botExcludePrSQL filters out bot accounts using the "pr" table alias.
	botExcludePrSQL = `AND pr.username NOT LIKE '%[bot]'
		AND LOWER(pr.username) NOT IN ('copilot','github-copilot','claude','anthropic-claude')`

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
		"GITHUB":                "MICROSOFT",
		"MICROSOFT CHINA":       "MICROSOFT",
		"MICROSOFT CORPORATION": "MICROSOFT",
		"XAMARIN":               "MICROSOFT",

		// ---- NVIDIA ----
		"NVIDIA CORPORATION": "NVIDIA",

		// ---- Oracle ----
		"OCI":                          "ORACLE",
		"ORACLE AMERICA":               "ORACLE",
		"ORACLE CLOUD INFRASTRUCTURE":  "ORACLE",
		"ORACLE OCI":                   "ORACLE",
		"WERCKER":                      "ORACLE",

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

func sinceDate(months int) string {
	return time.Now().UTC().AddDate(0, -months, 0).Format("2006-01-02")
}

func rollbackTransaction(tx *sql.Tx) {
	if err := tx.Rollback(); err != nil {
		slog.Error("error rolling back transaction", "error", err)
	}
}
