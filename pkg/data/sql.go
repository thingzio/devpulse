package data

// Shared SQL fragments for contributor eligibility filtering.
// These ensure consistent bot exclusion and fork filtering across all packages
// that count or select contributors (reputation scoring, admin views, UI overview).

const (
	// BotNames is the list of known bot/AI usernames excluded from contributor counts.
	// Used in SQL via LOWER(col) NOT IN (...).
	BotNames = "'copilot','github-copilot','claude','anthropic-claude'"

	// ContribExcludeSQL filters out bots and fork-only events using "e" (event)
	// table alias. Use inside CASE WHEN or WHERE clauses that join event.
	ContribExcludeSQL = `e.type != 'fork'
		                   AND e.username NOT LIKE '%[bot]'
		                   AND LOWER(e.username) NOT IN (` + BotNames + `)`

	// ContribExcludeDSQL is the same filter using "d" (developer) table alias
	// for the username column. Fork exclusion still references "e" table.
	ContribExcludeDSQL = `e.type != 'fork'
		                   AND d.username NOT LIKE '%[bot]'
		                   AND LOWER(d.username) NOT IN (` + BotNames + `)`
)
