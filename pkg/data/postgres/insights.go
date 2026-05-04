package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"time"

	"github.com/thingzio/devpulse/pkg/data"
)

const (
	// selectRetentionTpl: $1=since, dynamic org/repo/entity via queryBuilder
	// %[1]s = GroupExpr(gran, "e.date"), %[2]s = whereClause
	selectRetentionTpl = `WITH first_seen AS (
			SELECT e.username, MIN(%[1]s) AS first_period
			FROM devpulse_event e
			JOIN devpulse_developer d ON e.username = d.username
			WHERE e.date >= $1
			  ` + botExcludeTpl + `
			  ` + forkExcludeSQL + `
			  %[2]s
			GROUP BY e.username
		),
		periods AS (
			SELECT DISTINCT e.username, %[1]s AS period
			FROM devpulse_event e
			JOIN devpulse_developer d ON e.username = d.username
			WHERE e.date >= $1
			  ` + botExcludeTpl + `
			  ` + forkExcludeSQL + `
			  %[2]s
		)
		SELECT p.period,
			SUM(CASE WHEN f.first_period = p.period THEN 1 ELSE 0 END) AS new_contributors,
			SUM(CASE WHEN f.first_period < p.period THEN 1 ELSE 0 END) AS returning_contributors
		FROM periods p
		JOIN first_seen f ON p.username = f.username
		GROUP BY p.period
		ORDER BY p.period
	`

	// selectTimeToMergeTpl: $1=since, dynamic org/repo/entity via queryBuilder
	// %[1]s = GroupExpr(gran, "e.created_at"), %[2]s = whereClause
	// COUNT(DISTINCT number) is defensive against any residual duplicate
	// rows for the same PR (the import path keys by created_at::date now,
	// but the count must not double-count if dedupe drift ever returns).
	selectTimeToMergeTpl = `SELECT
			%[1]s AS period,
			COUNT(DISTINCT e.number) AS cnt,
			AVG(EXTRACT(EPOCH FROM (e.merged_at::timestamp - e.created_at::timestamp)) / 86400.0) AS avg_days
		FROM devpulse_event e
		JOIN devpulse_developer d ON e.username = d.username
		WHERE e.type = 'pr'
		  AND e.number IS NOT NULL
		  AND e.merged_at IS NOT NULL
		  AND e.created_at IS NOT NULL
		  AND e.created_at >= $1
		  ` + botExcludeTpl + `
		  %[2]s
		GROUP BY period
		ORDER BY period
	`

	// selectTimeToRestoreBugsTpl: $1=since, dynamic org/repo/entity via queryBuilder
	// %[1]s = GroupExpr(gran, "e.created_at"), %[2]s = whereClause
	selectTimeToRestoreBugsTpl = `SELECT
			%[1]s AS period,
			COUNT(DISTINCT e.number) AS cnt,
			AVG(EXTRACT(EPOCH FROM (e.closed_at::timestamp - e.created_at::timestamp)) / 86400.0) AS avg_days
		FROM devpulse_event e
		JOIN devpulse_developer d ON e.username = d.username
		WHERE e.type = 'issue'
		  AND e.number IS NOT NULL
		  AND e.closed_at IS NOT NULL
		  AND e.created_at IS NOT NULL
		  AND e.state = 'closed'
		  AND LOWER(e.labels) LIKE '%%bug%%'
		  AND EXISTS (
		      SELECT 1 FROM devpulse_release r
		      WHERE r.org = e.org AND r.repo = e.repo
		        AND r.published_at::timestamp BETWEEN e.created_at::timestamp - INTERVAL '7 days' AND e.created_at::timestamp
		  )
		  AND e.created_at >= $1
		  ` + botExcludeTpl + `
		  %[2]s
		GROUP BY period
		ORDER BY period
	`

	// selectTimeToCloseTpl: $1=since, dynamic org/repo/entity via queryBuilder
	// %[1]s = GroupExpr(gran, "e.created_at"), %[2]s = whereClause
	selectTimeToCloseTpl = `SELECT
			%[1]s AS period,
			COUNT(DISTINCT e.number) AS cnt,
			AVG(EXTRACT(EPOCH FROM (e.closed_at::timestamp - e.created_at::timestamp)) / 86400.0) AS avg_days
		FROM devpulse_event e
		JOIN devpulse_developer d ON e.username = d.username
		WHERE e.type = 'issue'
		  AND e.number IS NOT NULL
		  AND e.closed_at IS NOT NULL
		  AND e.created_at IS NOT NULL
		  AND e.state = 'closed'
		  AND e.created_at >= $1
		  ` + botExcludeTpl + `
		  %[2]s
		GROUP BY period
		ORDER BY period
	`

	// selectForksAndActivityTpl: $1=since, dynamic org/repo/entity via queryBuilder
	// %[1]s = GroupExpr(gran, "e.date"), %[2]s = whereClause
	selectForksAndActivityTpl = `SELECT
			%[1]s AS period,
			SUM(CASE WHEN e.type = 'fork' THEN 1 ELSE 0 END) AS forks,
			COUNT(*) AS events
		FROM devpulse_event e
		JOIN devpulse_developer d ON e.username = d.username
		WHERE e.date >= $1
		  ` + botExcludeTpl + `
		  %[2]s
		GROUP BY period
		ORDER BY period
	`

	// selectPRReviewRatioTpl: $1=pr_type, $2=review_type, $3=since, dynamic org/repo/entity via queryBuilder
	// %[1]s = GroupExpr(gran, "e.date"), %[2]s = whereClause
	selectPRReviewRatioTpl = `SELECT
			%[1]s AS period,
			SUM(CASE WHEN e.type = $1 THEN 1 ELSE 0 END) AS prs,
			SUM(CASE WHEN e.type = $2 THEN 1 ELSE 0 END) AS reviews
		FROM devpulse_event e
		JOIN devpulse_developer d ON e.username = d.username
		WHERE e.type IN ($1, $2)
		  AND e.date >= $3
		  ` + botExcludeTpl + `
		  %[2]s
		GROUP BY period
		ORDER BY period
	`

	// selectChangeFailuresTpl: $1=since, dynamic org/repo/entity via queryBuilder
	// %[1]s = GroupExpr(gran, "e.created_at"), %[2]s = whereClause
	selectChangeFailuresTpl = `SELECT
		%[1]s AS period,
		COUNT(DISTINCT (e.org, e.repo, e.type, e.number)) AS failures
	FROM devpulse_event e
	JOIN devpulse_developer d ON e.username = d.username
	WHERE (
	    (e.type = 'issue' AND LOWER(e.labels) LIKE '%%bug%%'
	     AND EXISTS (
	        SELECT 1 FROM devpulse_release r
	        WHERE r.org = e.org AND r.repo = e.repo
	          AND r.published_at::timestamp BETWEEN e.created_at::timestamp - INTERVAL '7 days' AND e.created_at::timestamp
	     ))
	    OR
	    (e.type = 'pr' AND LOWER(e.title) LIKE '%%revert%%')
	)
	  AND e.number IS NOT NULL
	  AND e.created_at >= $1
	  ` + botExcludeTpl + `
	  %[2]s
	GROUP BY period
	ORDER BY period
	`

	// selectDeploymentCountTpl: $1=since, dynamic org/repo via queryBuilder (no entity — no developer join)
	// %[1]s = GroupExpr(gran, "published_at"), %[2]s = whereClause
	selectDeploymentCountTpl = `SELECT
		%[1]s AS period,
		COUNT(*) AS cnt
	FROM devpulse_release
	WHERE published_at >= $1
	  %[2]s
	GROUP BY period
	ORDER BY period
	`

	// selectReviewLatencyTpl: $1=since, dynamic org/repo/entity via queryBuilder
	// %[1]s = GroupExpr(gran, "date"), %[2]s = GroupExpr(gran, "pr.created_at"), %[3]s = whereClause
	selectReviewLatencyTpl = `WITH periods AS (
		SELECT DISTINCT %[1]s AS period
		FROM devpulse_event
		WHERE date >= $1
	),
	latency AS (
		SELECT
			%[2]s AS period,
			(EXTRACT(EPOCH FROM (MIN(rev.created_at::timestamp) - MIN(pr.created_at::timestamp))) / 3600.0) AS hours
		FROM devpulse_event pr
		JOIN devpulse_event rev ON pr.org = rev.org AND pr.repo = rev.repo AND pr.number = rev.number
			AND rev.type = 'pr_review'
		JOIN devpulse_developer d ON pr.username = d.username
		WHERE pr.type = 'pr'
		  AND pr.number IS NOT NULL
		  AND pr.created_at IS NOT NULL
		  AND rev.created_at IS NOT NULL
		  AND pr.created_at >= $1
		  ` + botExcludePrTpl + `
		  %[3]s
		GROUP BY pr.org, pr.repo, pr.number, pr.created_at
	)
	SELECT
		p.period,
		COALESCE(COUNT(l.hours), 0) AS cnt,
		COALESCE(AVG(l.hours), 0) AS avg_hours
	FROM periods p
	LEFT JOIN latency l ON p.period = l.period
	GROUP BY p.period
	ORDER BY p.period
	`

	// selectPRSizeDistributionTpl: $1=since, dynamic org/repo/entity via queryBuilder
	// %[1]s = GroupExpr(gran, "e.created_at"), %[2]s = whereClause
	selectPRSizeDistributionTpl = `SELECT
		%[1]s AS period,
		SUM(CASE WHEN COALESCE(e.additions, 0) + COALESCE(e.deletions, 0) < 50 THEN 1 ELSE 0 END) AS small,
		SUM(CASE WHEN COALESCE(e.additions, 0) + COALESCE(e.deletions, 0) BETWEEN 50 AND 249 THEN 1 ELSE 0 END) AS medium,
		SUM(CASE WHEN COALESCE(e.additions, 0) + COALESCE(e.deletions, 0) BETWEEN 250 AND 999 THEN 1 ELSE 0 END) AS large,
		SUM(CASE WHEN COALESCE(e.additions, 0) + COALESCE(e.deletions, 0) >= 1000 THEN 1 ELSE 0 END) AS xlarge
	FROM devpulse_event e
	JOIN devpulse_developer d ON e.username = d.username
	WHERE e.type = 'pr'
	  AND e.created_at IS NOT NULL
	  AND e.created_at >= $1
	  ` + botExcludeTpl + `
	  %[2]s
	GROUP BY period
	ORDER BY period
	`

	// selectContributorMomentumTpl: $1=since, dynamic org/repo/entity via queryBuilder
	// %[1]s = GroupExpr(gran, "date"), %[2]s = MomentumInterval(gran), %[3]s = MomentumFormat(gran), %[4]s = whereClause
	selectContributorMomentumTpl = `WITH periods AS (
		SELECT DISTINCT %[1]s AS period
		FROM devpulse_event
		WHERE date >= $1
	)
	SELECT
		p.period,
		COUNT(DISTINCT e.username) AS active
	FROM periods p
	JOIN devpulse_event e ON %[1]s >= TO_CHAR(((CASE WHEN length(p.period) = 7 THEN p.period || '-01' ELSE p.period END)::date - INTERVAL '%[2]s'), %[3]s)
		AND %[1]s <= p.period
	JOIN devpulse_developer d ON e.username = d.username
	WHERE 1=1
	  ` + botExcludeTpl + `
	  ` + forkExcludeSQL + `
	  %[4]s
	GROUP BY p.period
	ORDER BY p.period
	`

	// selectContributorFunnelTpl: $1=since, dynamic org/repo/entity via queryBuilder
	// %[1]s = GroupExpr(gran, "date")
	// %[2]s = GroupExpr(gran, "f.first_comment")
	// %[3]s = GroupExpr(gran, "f.first_pr")
	// %[4]s = GroupExpr(gran, "f.first_merge")
	// %[5]s = whereClause
	selectContributorFunnelTpl = `WITH firsts AS (
		SELECT
			e.username,
			MIN(CASE WHEN e.type = 'issue_comment' THEN e.date END) AS first_comment,
			MIN(CASE WHEN e.type = 'pr' THEN e.date END) AS first_pr,
			MIN(CASE WHEN e.type = 'pr' AND e.state = 'merged' THEN e.date END) AS first_merge
		FROM devpulse_event e
		JOIN devpulse_developer d ON e.username = d.username
		WHERE 1=1
		  ` + botExcludeTpl + `
		  %[5]s
		GROUP BY e.username
	),
	periods AS (
		SELECT DISTINCT %[1]s AS period FROM devpulse_event WHERE date >= $1
	)
	SELECT
		p.period,
		(SELECT COUNT(*) FROM firsts f WHERE f.first_comment IS NOT NULL AND %[2]s = p.period),
		(SELECT COUNT(*) FROM firsts f WHERE f.first_pr IS NOT NULL AND %[3]s = p.period),
		(SELECT COUNT(*) FROM firsts f WHERE f.first_merge IS NOT NULL AND %[4]s = p.period)
	FROM periods p
	WHERE EXISTS (
		SELECT 1 FROM firsts f WHERE
			(f.first_comment IS NOT NULL AND %[2]s = p.period) OR
			(f.first_pr IS NOT NULL AND %[3]s = p.period) OR
			(f.first_merge IS NOT NULL AND %[4]s = p.period)
	)
	ORDER BY p.period
	`

	// selectContributorProfileTpl: $1=username, $2=since, dynamic org/repo/entity via queryBuilder
	// %[1]s = whereClause
	selectContributorProfileTpl = `WITH user_counts AS (
		SELECT
			SUM(CASE WHEN e.type = 'pr' THEN 1 ELSE 0 END) AS prs_opened,
			SUM(CASE WHEN e.type = 'pr' AND e.state = 'merged' THEN 1 ELSE 0 END) AS prs_merged,
			SUM(CASE WHEN e.type = 'pr_review' THEN 1 ELSE 0 END) AS pr_reviews,
			SUM(CASE WHEN e.type = 'issue' THEN 1 ELSE 0 END) AS issues_opened,
			SUM(CASE WHEN e.type = 'issue_comment' THEN 1 ELSE 0 END) AS issue_comments,
			SUM(CASE WHEN e.type = 'pr' AND COALESCE(e.additions, 0) + COALESCE(e.deletions, 0) < 50 THEN 1 ELSE 0 END) AS pr_small,
			SUM(CASE WHEN e.type = 'pr' AND COALESCE(e.additions, 0) + COALESCE(e.deletions, 0) BETWEEN 50 AND 249 THEN 1 ELSE 0 END) AS pr_medium,
			SUM(CASE WHEN e.type = 'pr' AND COALESCE(e.additions, 0) + COALESCE(e.deletions, 0) BETWEEN 250 AND 999 THEN 1 ELSE 0 END) AS pr_large,
			SUM(CASE WHEN e.type = 'pr' AND COALESCE(e.additions, 0) + COALESCE(e.deletions, 0) >= 1000 THEN 1 ELSE 0 END) AS pr_xlarge
		FROM devpulse_event e
		JOIN devpulse_developer d ON e.username = d.username
		WHERE e.username = $1
		  AND e.date >= $2
		  %[1]s
	),
	per_user AS (
		SELECT
			e.username,
			SUM(CASE WHEN e.type = 'pr' THEN 1 ELSE 0 END) AS prs_opened,
			SUM(CASE WHEN e.type = 'pr' AND e.state = 'merged' THEN 1 ELSE 0 END) AS prs_merged,
			SUM(CASE WHEN e.type = 'pr_review' THEN 1 ELSE 0 END) AS pr_reviews,
			SUM(CASE WHEN e.type = 'issue' THEN 1 ELSE 0 END) AS issues_opened,
			SUM(CASE WHEN e.type = 'issue_comment' THEN 1 ELSE 0 END) AS issue_comments,
			SUM(CASE WHEN e.type = 'pr' AND COALESCE(e.additions, 0) + COALESCE(e.deletions, 0) < 50 THEN 1 ELSE 0 END) AS pr_small,
			SUM(CASE WHEN e.type = 'pr' AND COALESCE(e.additions, 0) + COALESCE(e.deletions, 0) BETWEEN 50 AND 249 THEN 1 ELSE 0 END) AS pr_medium,
			SUM(CASE WHEN e.type = 'pr' AND COALESCE(e.additions, 0) + COALESCE(e.deletions, 0) BETWEEN 250 AND 999 THEN 1 ELSE 0 END) AS pr_large,
			SUM(CASE WHEN e.type = 'pr' AND COALESCE(e.additions, 0) + COALESCE(e.deletions, 0) >= 1000 THEN 1 ELSE 0 END) AS pr_xlarge
		FROM devpulse_event e
		JOIN devpulse_developer d ON e.username = d.username
		WHERE e.date >= $2
		  ` + botExcludeTpl + `
		  %[1]s
		GROUP BY e.username
	),
	median_counts AS (
		SELECT
			PERCENTILE_CONT(0.5) WITHIN GROUP (ORDER BY prs_opened) AS prs_opened,
			PERCENTILE_CONT(0.5) WITHIN GROUP (ORDER BY prs_merged) AS prs_merged,
			PERCENTILE_CONT(0.5) WITHIN GROUP (ORDER BY pr_reviews) AS pr_reviews,
			PERCENTILE_CONT(0.5) WITHIN GROUP (ORDER BY issues_opened) AS issues_opened,
			PERCENTILE_CONT(0.5) WITHIN GROUP (ORDER BY issue_comments) AS issue_comments,
			PERCENTILE_CONT(0.5) WITHIN GROUP (ORDER BY pr_small) AS pr_small,
			PERCENTILE_CONT(0.5) WITHIN GROUP (ORDER BY pr_medium) AS pr_medium,
			PERCENTILE_CONT(0.5) WITHIN GROUP (ORDER BY pr_large) AS pr_large,
			PERCENTILE_CONT(0.5) WITHIN GROUP (ORDER BY pr_xlarge) AS pr_xlarge
		FROM per_user
	)
	SELECT
		COALESCE(u.prs_opened, 0), COALESCE(u.prs_merged, 0), COALESCE(u.pr_reviews, 0),
		COALESCE(u.issues_opened, 0), COALESCE(u.issue_comments, 0),
		COALESCE(u.pr_small, 0), COALESCE(u.pr_medium, 0), COALESCE(u.pr_large, 0), COALESCE(u.pr_xlarge, 0),
		COALESCE(m.prs_opened, 0), COALESCE(m.prs_merged, 0), COALESCE(m.pr_reviews, 0),
		COALESCE(m.issues_opened, 0), COALESCE(m.issue_comments, 0),
		COALESCE(m.pr_small, 0), COALESCE(m.pr_medium, 0), COALESCE(m.pr_large, 0), COALESCE(m.pr_xlarge, 0)
	FROM user_counts u, median_counts m
	`

	// selectInsightsSummaryTpl combines bus factor, pony factor, and banner stats
	// into a single CTE to avoid scanning devpulse_event+devpulse_developer 3 times.
	// $1=since, dynamic org/repo/entity via queryBuilder
	selectInsightsSummaryTpl = `WITH base AS (
		SELECT e.org, e.repo, e.username, d.entity
		FROM devpulse_event e
		JOIN devpulse_developer d ON e.username = d.username
		WHERE e.date >= $1
		  ` + botExcludeTpl + `
		  ` + forkExcludeSQL + `
		  %s
	),
	dev_counts AS (
		SELECT username, COUNT(*) AS cnt
		FROM base
		GROUP BY username
		ORDER BY cnt DESC
	),
	dev_running AS (
		SELECT cnt,
			SUM(cnt) OVER (ORDER BY cnt DESC) AS cumsum,
			SUM(cnt) OVER () AS total
		FROM dev_counts
	),
	ent_counts AS (
		SELECT entity, COUNT(*) AS cnt
		FROM base
		WHERE entity IS NOT NULL AND entity != ''
		GROUP BY entity
		ORDER BY cnt DESC
	),
	ent_running AS (
		SELECT cnt,
			SUM(cnt) OVER (ORDER BY cnt DESC) AS cumsum,
			SUM(cnt) OVER () AS total
		FROM ent_counts
	)
	SELECT
		COALESCE((SELECT COUNT(*) FROM dev_running WHERE cumsum - cnt < total * 0.5), 0),
		COALESCE((SELECT COUNT(*) FROM ent_running WHERE cumsum - cnt < total * 0.5), 0),
		COUNT(DISTINCT org),
		COUNT(DISTINCT org || '/' || repo),
		COUNT(*),
		COUNT(DISTINCT username),
		COALESCE((SELECT MAX(last_import_at) FROM devpulse_repo_meta), '')
	FROM base
	`

	// selectIssueOpenCloseRatioTpl: $1=since, dynamic org/repo/entity via queryBuilder
	// %[1]s = GroupExpr(gran, "e.created_at"), %[2]s = GroupExpr(gran, "e.closed_at"), %[3]s = whereClause
	selectIssueOpenCloseRatioTpl = `SELECT period, SUM(opened) AS opened, SUM(closed) AS closed
		FROM (
			SELECT %[1]s AS period, 1 AS opened, 0 AS closed
			FROM devpulse_event e
			JOIN devpulse_developer d ON e.username = d.username
			WHERE e.type = 'issue'
			  AND e.created_at IS NOT NULL
			  AND e.created_at >= $1
			  ` + botExcludeTpl + `
			  %[3]s
			UNION ALL
			SELECT %[2]s AS period, 0 AS opened, 1 AS closed
			FROM devpulse_event e
			JOIN devpulse_developer d ON e.username = d.username
			WHERE e.type = 'issue'
			  AND e.closed_at IS NOT NULL
			  AND e.closed_at >= $1
			  ` + botExcludeTpl + `
			  %[3]s
		) sub
		GROUP BY period
		ORDER BY period
	`

	// selectTimeToFirstResponseTpl: $1=since, dynamic org/repo/entity via queryBuilder
	// %[1]s = GroupExpr(gran, "e.created_at"), %[2]s = whereClause
	selectTimeToFirstResponseTpl = `WITH issue_first AS (
		SELECT
			e.org, e.repo, e.number,
			%[1]s AS period,
			MIN(
				EXTRACT(EPOCH FROM (c.created_at::timestamp - e.created_at::timestamp)) / 3600.0
			) AS hours_to_first
		FROM devpulse_event e
		JOIN devpulse_event c ON c.org = e.org AND c.repo = e.repo AND c.number = e.number
			AND c.type = 'issue_comment' AND c.created_at > e.created_at
		JOIN devpulse_developer d ON e.username = d.username
		WHERE e.type = 'issue'
		  AND e.created_at IS NOT NULL
		  AND e.number IS NOT NULL
		  AND e.created_at >= $1
		  ` + botExcludeTpl + `
		  %[2]s
		GROUP BY e.org, e.repo, e.number, period
	), pr_first AS (
		SELECT
			e.org, e.repo, e.number,
			%[1]s AS period,
			MIN(
				EXTRACT(EPOCH FROM (c.created_at::timestamp - e.created_at::timestamp)) / 3600.0
			) AS hours_to_first
		FROM devpulse_event e
		JOIN devpulse_event c ON c.org = e.org AND c.repo = e.repo AND c.number = e.number
			AND c.type = 'pr_review' AND c.created_at > e.created_at
		JOIN devpulse_developer d ON e.username = d.username
		WHERE e.type = 'pr'
		  AND e.created_at IS NOT NULL
		  AND e.number IS NOT NULL
		  AND e.created_at >= $1
		  ` + botExcludeTpl + `
		  %[2]s
		GROUP BY e.org, e.repo, e.number, period
	)
	SELECT
		COALESCE(i.period, p.period) AS period,
		COALESCE(i.avg_hours, 0),
		COALESCE(p.avg_hours, 0)
	FROM (SELECT period, AVG(hours_to_first) AS avg_hours FROM issue_first GROUP BY period) i
	FULL OUTER JOIN (SELECT period, AVG(hours_to_first) AS avg_hours FROM pr_first GROUP BY period) p
		ON i.period = p.period
	ORDER BY period
`

	// selectAgingPRsTpl: $1=since, dynamic org/repo/entity via queryBuilder.
	// COUNT(DISTINCT number) defends against duplicate event rows ever
	// re-emerging (the original UpdatedAt-key bug counted each stale 'open'
	// snapshot of a single PR separately).
	selectAgingPRsTpl = `SELECT
		COUNT(DISTINCT e.number) AS total_open,
		COUNT(DISTINCT e.number) FILTER (WHERE EXTRACT(EPOCH FROM (NOW() - e.created_at::timestamp)) / 86400.0 > 30) AS over_30,
		COUNT(DISTINCT e.number) FILTER (WHERE EXTRACT(EPOCH FROM (NOW() - e.created_at::timestamp)) / 86400.0 > 90) AS over_90
	FROM devpulse_event e
	JOIN devpulse_developer d ON e.username = d.username
	WHERE e.type = 'pr'
	  AND e.number IS NOT NULL
	  AND (e.state IS NULL OR e.state NOT IN ('merged', 'closed'))
	  AND e.created_at IS NOT NULL
	  AND e.created_at >= $1
	  ` + botExcludeTpl + `
	  %s
	`

	// selectUnansweredRateTpl: $1=since, dynamic org/repo/entity via queryBuilder.
	// Filters out merged/closed items — once a PR or issue reaches a terminal
	// state it no longer "needs a response", so counting it inflates the
	// metric and misleads the AI narrative ("N items requiring response"
	// when most have already been resolved).
	selectUnansweredRateTpl = `WITH items AS (
    SELECT e.org, e.repo, e.number, e.type, e.username, e.created_at
    FROM devpulse_event e
    JOIN devpulse_developer d ON e.username = d.username
    WHERE e.type IN ('issue', 'pr')
      AND e.number IS NOT NULL
      AND (e.state IS NULL OR e.state NOT IN ('merged', 'closed'))
      AND e.created_at IS NOT NULL
      AND EXTRACT(EPOCH FROM (NOW() - e.created_at::timestamp)) / 86400.0 > 7
      AND e.created_at >= $1
      ` + botExcludeTpl + `
      %s
),
responded AS (
    SELECT DISTINCT i.org, i.repo, i.number
    FROM items i
    JOIN devpulse_event r ON r.org = i.org AND r.repo = i.repo AND r.number = i.number
      AND r.type IN ('issue_comment', 'pr_review')
      AND r.username != i.username
      AND r.created_at > i.created_at
)
SELECT
    (SELECT COUNT(DISTINCT (i.org, i.repo, i.number)) FROM items i) AS total,
    (SELECT COUNT(DISTINCT (i.org, i.repo, i.number)) FROM items i
     WHERE NOT EXISTS (SELECT 1 FROM responded r WHERE r.org = i.org AND r.repo = i.repo AND r.number = i.number)
    ) AS unanswered
`

	// selectResponseSLOTpl: $1=since, dynamic org/repo/entity via queryBuilder
	selectResponseSLOTpl = `WITH first_response AS (
    SELECT e.org, e.repo, e.number,
        MIN(EXTRACT(EPOCH FROM (r.created_at::timestamp - e.created_at::timestamp)) / 3600.0) AS hours
    FROM devpulse_event e
    JOIN devpulse_event r ON r.org = e.org AND r.repo = e.repo AND r.number = e.number
        AND r.type IN ('issue_comment', 'pr_review')
        AND r.username != e.username
        AND r.created_at > e.created_at
    JOIN devpulse_developer d ON e.username = d.username
    WHERE e.type IN ('issue', 'pr')
      AND e.number IS NOT NULL
      AND e.created_at IS NOT NULL
      AND e.created_at >= $1
      ` + botExcludeTpl + `
      %s
    GROUP BY e.org, e.repo, e.number
)
SELECT
    COUNT(*) AS total,
    COALESCE(SUM(CASE WHEN hours <= 48 THEN 1 ELSE 0 END), 0) AS within_slo
FROM first_response
`

	// selectPortfolioSummaryTpl: dynamic org/repo via queryBuilder
	// %[1]s = orgRepoWhere for repo_meta, %[2]s = orgRepoWhere for metric_history,
	// %[3]s = orgRepoWhere for event (e. prefix)
	selectPortfolioSummaryTpl = `WITH current_totals AS (
    SELECT
        COALESCE(SUM(stars), 0) AS stars,
        COALESCE(SUM(forks), 0) AS forks,
        COALESCE(SUM(open_issues), 0) AS open_issues
    FROM devpulse_repo_meta
    WHERE 1=1 %[1]s
),
prev_snapshot AS (
    SELECT
        COALESCE(SUM(h.stars), 0) AS stars,
        COALESCE(SUM(h.forks), 0) AS forks
    FROM (
        SELECT DISTINCT ON (org, repo) org, repo, stars, forks
        FROM devpulse_repo_metric_history
        WHERE date <= $2 %[2]s
        ORDER BY org, repo, date DESC
    ) h
),
pr_stats AS (
    SELECT
        COUNT(*) FILTER (WHERE e.state IN ('merged', 'closed')) AS closed_prs,
        COUNT(DISTINCT e.username) AS contributors,
        COALESCE(AVG(EXTRACT(EPOCH FROM (e.merged_at::timestamp - e.created_at::timestamp)) / 3600.0)
            FILTER (WHERE e.merged_at IS NOT NULL AND e.created_at IS NOT NULL), 0) AS avg_merge_h,
        COALESCE(PERCENTILE_CONT(0.5) WITHIN GROUP (
            ORDER BY EXTRACT(EPOCH FROM (e.merged_at::timestamp - e.created_at::timestamp)) / 3600.0
        ) FILTER (WHERE e.merged_at IS NOT NULL AND e.created_at IS NOT NULL), 0) AS median_merge_h
    FROM devpulse_event e
    WHERE e.type = 'pr'
      AND e.date >= $1
      ` + botExcludeTpl + `
      %[3]s
)
SELECT c.stars, c.forks, c.open_issues,
       c.stars - p.stars, c.forks - p.forks,
       ps.closed_prs, ps.contributors,
       ps.avg_merge_h, ps.median_merge_h
FROM current_totals c, prev_snapshot p, pr_stats ps
`

	// selectSignalsTpl: $1=7_days_ago, $2=14_days_ago, $3=limit
	// %[1]s = org filter for this_week, %[2]s = org filter for last_week
	selectSignalsTpl = `WITH this_week AS (
    SELECT e.org, e.repo, COUNT(*) AS events
    FROM devpulse_event e
    WHERE e.date >= $1
      ` + botExcludeTpl + `
      ` + forkExcludeSQL + `
      %[1]s
    GROUP BY e.org, e.repo
),
last_week AS (
    SELECT e.org, e.repo, COUNT(*) AS events
    FROM devpulse_event e
    WHERE e.date >= $2 AND e.date < $1
      ` + botExcludeTpl + `
      ` + forkExcludeSQL + `
      %[2]s
    GROUP BY e.org, e.repo
)
SELECT
    COALESCE(t.org, l.org) AS org,
    COALESCE(t.repo, l.repo) AS repo,
    COALESCE(t.events, 0) - COALESCE(l.events, 0) AS delta,
    CASE WHEN COALESCE(l.events, 0) > 0
        THEN (COALESCE(t.events, 0) - l.events)::float / l.events * 100
        ELSE 0 END AS delta_pct
FROM this_week t
FULL OUTER JOIN last_week l ON t.org = l.org AND t.repo = l.repo
WHERE ABS(COALESCE(t.events, 0) - COALESCE(l.events, 0)) > 0
ORDER BY ABS(COALESCE(t.events, 0) - COALESCE(l.events, 0)) DESC
LIMIT $3
`

	// selectDailyActivityTpl: $1=since, dynamic org/repo/entity via queryBuilder
	selectDailyActivityTpl = `SELECT e.date, COUNT(*) AS cnt
		FROM devpulse_event e
		JOIN devpulse_developer d ON e.username = d.username
		WHERE e.date >= $1
		  ` + botExcludeTpl + `
		  ` + forkExcludeSQL + `
		  %s
		GROUP BY e.date
		ORDER BY e.date
	`
)

func (s *Store) GetInsightsSummary(ctx context.Context, org, repo, entity *string, days int) (*data.InsightsSummary, error) {
	if s.db == nil {
		return nil, data.ErrDBNotInitialized
	}

	since := sinceDate(days)
	summary := &data.InsightsSummary{}

	qb := newQueryBuilder(2)
	qb.addOptional("e.org", org)
	qb.addOptional("e.repo", repo)
	qb.addOptional("d.entity", entity)
	query := fmt.Sprintf(selectInsightsSummaryTpl, qb.whereClause())
	args := append([]any{since}, qb.args...)

	if err := s.db.QueryRowContext(ctx, query, args...).Scan(
		&summary.BusFactor, &summary.PonyFactor,
		&summary.Orgs, &summary.Repos, &summary.Events, &summary.Contributors,
		&summary.LastImport,
	); err != nil {
		return nil, fmt.Errorf("failed to query insights summary: %w", err)
	}

	return summary, nil
}

func (s *Store) GetDailyActivity(ctx context.Context, org, repo, entity *string, days int) (*data.DailyActivitySeries, error) {
	if s.db == nil {
		return nil, data.ErrDBNotInitialized
	}

	since := sinceDate(days)
	qb := newQueryBuilder(2)
	qb.addOptional("e.org", org)
	qb.addOptional("e.repo", repo)
	qb.addOptional("d.entity", entity)
	query := fmt.Sprintf(selectDailyActivityTpl, qb.whereClause())
	args := append([]any{since}, qb.args...)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query daily activity: %w", err)
	}
	defer rows.Close()

	series := &data.DailyActivitySeries{}
	for rows.Next() {
		var date string
		var count int
		if err := rows.Scan(&date, &count); err != nil {
			return nil, fmt.Errorf("failed to scan daily activity row: %w", err)
		}
		series.Dates = append(series.Dates, date)
		series.Counts = append(series.Counts, count)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating rows: %w", err)
	}

	return series, nil
}

func getDualSeries[T int | float64](ctx context.Context, db DBTX, queryTpl string, cols []string, org, repo, entity *string, days int) ([]string, []T, []T, error) {
	if db == nil {
		return nil, nil, nil, data.ErrDBNotInitialized
	}

	gran := AutoGranularity(days)
	since := sinceDate(days)
	qb := newQueryBuilder(2)
	qb.addOptional("e.org", org)
	qb.addOptional("e.repo", repo)
	qb.addOptional("d.entity", entity)

	fmtArgs := make([]any, len(cols)+1)
	for i, col := range cols {
		fmtArgs[i] = GroupExpr(gran, col)
	}
	fmtArgs[len(cols)] = qb.whereClause()
	query := fmt.Sprintf(queryTpl, fmtArgs...)
	args := append([]any{since}, qb.args...)

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to query dual series: %w", err)
	}
	defer rows.Close()

	var ms []string
	var a, b []T

	for rows.Next() {
		var label string
		var v1, v2 T
		if err := rows.Scan(&label, &v1, &v2); err != nil {
			return nil, nil, nil, fmt.Errorf("failed to scan dual row: %w", err)
		}
		ms = append(ms, label)
		a = append(a, v1)
		b = append(b, v2)
	}

	if err := rows.Err(); err != nil {
		return nil, nil, nil, fmt.Errorf("error iterating rows: %w", err)
	}

	gf := newGapFiller(days, ms)
	return gf.periods, gapFillSlice(gf, a), gapFillSlice(gf, b), nil
}

func (s *Store) GetContributorRetention(ctx context.Context, org, repo, entity *string, days int) (*data.RetentionSeries, error) {
	ms, newC, retC, err := getDualSeries[int](ctx, s.db, selectRetentionTpl, []string{"e.date"}, org, repo, entity, days)
	if err != nil {
		return nil, err
	}
	return &data.RetentionSeries{Labels: ms, New: newC, Returning: retC}, nil
}

func (s *Store) GetPRReviewRatio(ctx context.Context, org, repo, entity *string, days int) (*data.PRReviewRatioSeries, error) {
	if s.db == nil {
		return nil, data.ErrDBNotInitialized
	}

	gran := AutoGranularity(days)
	since := sinceDate(days)
	qb := newQueryBuilder(4)
	qb.addOptional("e.org", org)
	qb.addOptional("e.repo", repo)
	qb.addOptional("d.entity", entity)
	query := fmt.Sprintf(selectPRReviewRatioTpl, GroupExpr(gran, "e.date"), qb.whereClause())
	args := append([]any{data.EventTypePR, data.EventTypePRReview, since}, qb.args...)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query PR review ratio: %w", err)
	}
	defer rows.Close()

	sr := &data.PRReviewRatioSeries{
		Labels:  make([]string, 0),
		PRs:     make([]int, 0),
		Reviews: make([]int, 0),
		Ratio:   make([]float64, 0),
	}

	for rows.Next() {
		var label string
		var prs, reviews int
		if err := rows.Scan(&label, &prs, &reviews); err != nil {
			return nil, fmt.Errorf("failed to scan PR review ratio row: %w", err)
		}
		sr.Labels = append(sr.Labels, label)
		sr.PRs = append(sr.PRs, prs)
		sr.Reviews = append(sr.Reviews, reviews)

		var ratio float64
		if prs > 0 {
			ratio = float64(reviews) / float64(prs)
		}
		sr.Ratio = append(sr.Ratio, ratio)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating rows: %w", err)
	}

	gf := newGapFiller(days, sr.Labels)
	sr.Labels = gf.periods
	sr.PRs = gf.fillInt(sr.PRs)
	sr.Reviews = gf.fillInt(sr.Reviews)
	sr.Ratio = gf.fillFloat64(sr.Ratio)

	return sr, nil
}

func (s *Store) GetChangeFailureRate(ctx context.Context, org, repo, entity *string, days int) (*data.ChangeFailureRateSeries, error) {
	if s.db == nil {
		return nil, data.ErrDBNotInitialized
	}

	gran := AutoGranularity(days)
	since := sinceDate(days)

	// Failures query — uses e.org/e.repo/d.entity
	failQB := newQueryBuilder(2)
	failQB.addOptional("e.org", org)
	failQB.addOptional("e.repo", repo)
	failQB.addOptional("d.entity", entity)
	failQuery := fmt.Sprintf(selectChangeFailuresTpl, GroupExpr(gran, "e.created_at"), failQB.whereClause())
	failArgs := append([]any{since}, failQB.args...)

	failureMap := make(map[string]int)

	rows, err := s.db.QueryContext(ctx, failQuery, failArgs...)
	if err != nil {
		return nil, fmt.Errorf("failed to query change failures: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var label string
		var failures int
		if scanErr := rows.Scan(&label, &failures); scanErr != nil {
			return nil, fmt.Errorf("failed to scan change failure row: %w", scanErr)
		}
		failureMap[label] = failures
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating rows: %w", err)
	}

	// Deployment query — uses org/repo (no developer join, no entity)
	deployQB := newQueryBuilder(2)
	deployQB.addOptional("org", org)
	deployQB.addOptional("repo", repo)
	deployQuery := fmt.Sprintf(selectDeploymentCountTpl, GroupExpr(gran, "published_at"), deployQB.whereClause())
	deployArgs := append([]any{since}, deployQB.args...)

	deployMap := make(map[string]int)

	dRows, err := s.db.QueryContext(ctx, deployQuery, deployArgs...)
	if err != nil {
		return nil, fmt.Errorf("failed to query deployment count: %w", err)
	}
	defer dRows.Close()

	for dRows.Next() {
		var label string
		var cnt int
		if scanErr := dRows.Scan(&label, &cnt); scanErr != nil {
			return nil, fmt.Errorf("failed to scan deployment count row: %w", scanErr)
		}
		deployMap[label] = cnt
	}

	if err := dRows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating rows: %w", err)
	}

	monthSet := make(map[string]bool)
	for m := range failureMap {
		monthSet[m] = true
	}
	for m := range deployMap {
		monthSet[m] = true
	}

	sortedMonths := make([]string, 0, len(monthSet))
	for m := range monthSet {
		sortedMonths = append(sortedMonths, m)
	}
	sort.Strings(sortedMonths)

	sr := &data.ChangeFailureRateSeries{
		Labels:      make([]string, 0, len(sortedMonths)),
		Failures:    make([]int, 0, len(sortedMonths)),
		Deployments: make([]int, 0, len(sortedMonths)),
		Rate:        make([]float64, 0, len(sortedMonths)),
	}

	for _, m := range sortedMonths {
		f := failureMap[m]
		d := deployMap[m]
		var rate float64
		if d > 0 {
			rate = float64(f) / float64(d) * 100
		}
		sr.Labels = append(sr.Labels, m)
		sr.Failures = append(sr.Failures, f)
		sr.Deployments = append(sr.Deployments, d)
		sr.Rate = append(sr.Rate, rate)
	}

	gf := newGapFiller(days, sr.Labels)
	sr.Labels = gf.periods
	sr.Failures = gf.fillInt(sr.Failures)
	sr.Deployments = gf.fillInt(sr.Deployments)
	sr.Rate = gf.fillFloat64(sr.Rate)

	return sr, nil
}

func (s *Store) GetReviewLatency(ctx context.Context, org, repo, entity *string, days int) (*data.ReviewLatencySeries, error) {
	if s.db == nil {
		return nil, data.ErrDBNotInitialized
	}

	gran := AutoGranularity(days)
	since := sinceDate(days)
	qb := newQueryBuilder(2)
	qb.addOptional("pr.org", org)
	qb.addOptional("pr.repo", repo)
	qb.addOptional("d.entity", entity)
	query := fmt.Sprintf(selectReviewLatencyTpl, GroupExpr(gran, "date"), GroupExpr(gran, "pr.created_at"), qb.whereClause())
	args := append([]any{since}, qb.args...)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query review latency: %w", err)
	}
	defer rows.Close()

	sr := &data.ReviewLatencySeries{
		Labels:   make([]string, 0),
		Count:    make([]int, 0),
		AvgHours: make([]float64, 0),
	}

	for rows.Next() {
		var label string
		var cnt int
		var avgHours float64
		if err := rows.Scan(&label, &cnt, &avgHours); err != nil {
			return nil, fmt.Errorf("failed to scan review latency row: %w", err)
		}
		sr.Labels = append(sr.Labels, label)
		sr.Count = append(sr.Count, cnt)
		sr.AvgHours = append(sr.AvgHours, avgHours)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating rows: %w", err)
	}

	gf := newGapFiller(days, sr.Labels)
	sr.Labels = gf.periods
	sr.Count = gf.fillInt(sr.Count)
	sr.AvgHours = gf.fillFloat64(sr.AvgHours)

	return sr, nil
}

func (s *Store) getVelocitySeries(ctx context.Context, queryTpl, col string, org, repo, entity *string, days int) (*data.VelocitySeries, error) {
	if s.db == nil {
		return nil, data.ErrDBNotInitialized
	}

	gran := AutoGranularity(days)
	since := sinceDate(days)
	qb := newQueryBuilder(2)
	qb.addOptional("e.org", org)
	qb.addOptional("e.repo", repo)
	qb.addOptional("d.entity", entity)
	query := fmt.Sprintf(queryTpl, GroupExpr(gran, col), qb.whereClause())
	args := append([]any{since}, qb.args...)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query velocity series: %w", err)
	}
	defer rows.Close()

	sr := &data.VelocitySeries{
		Labels:  make([]string, 0),
		Count:   make([]int, 0),
		AvgDays: make([]float64, 0),
	}

	for rows.Next() {
		var label string
		var cnt int
		var avgDays float64
		if err := rows.Scan(&label, &cnt, &avgDays); err != nil {
			return nil, fmt.Errorf("failed to scan velocity row: %w", err)
		}
		sr.Labels = append(sr.Labels, label)
		sr.Count = append(sr.Count, cnt)
		sr.AvgDays = append(sr.AvgDays, avgDays)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating rows: %w", err)
	}

	gf := newGapFiller(days, sr.Labels)
	sr.Labels = gf.periods
	sr.Count = gf.fillInt(sr.Count)
	sr.AvgDays = gf.fillFloat64(sr.AvgDays)

	return sr, nil
}

func (s *Store) GetTimeToMerge(ctx context.Context, org, repo, entity *string, days int) (*data.VelocitySeries, error) {
	return s.getVelocitySeries(ctx, selectTimeToMergeTpl, "e.created_at", org, repo, entity, days)
}

func (s *Store) GetTimeToClose(ctx context.Context, org, repo, entity *string, days int) (*data.VelocitySeries, error) {
	return s.getVelocitySeries(ctx, selectTimeToCloseTpl, "e.created_at", org, repo, entity, days)
}

func (s *Store) GetTimeToRestoreBugs(ctx context.Context, org, repo, entity *string, days int) (*data.VelocitySeries, error) {
	return s.getVelocitySeries(ctx, selectTimeToRestoreBugsTpl, "e.created_at", org, repo, entity, days)
}

func (s *Store) GetPRSizeDistribution(ctx context.Context, org, repo, entity *string, days int) (*data.PRSizeSeries, error) {
	if s.db == nil {
		return nil, data.ErrDBNotInitialized
	}

	gran := AutoGranularity(days)
	since := sinceDate(days)
	qb := newQueryBuilder(2)
	qb.addOptional("e.org", org)
	qb.addOptional("e.repo", repo)
	qb.addOptional("d.entity", entity)
	query := fmt.Sprintf(selectPRSizeDistributionTpl, GroupExpr(gran, "e.created_at"), qb.whereClause())
	args := append([]any{since}, qb.args...)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query PR size distribution: %w", err)
	}
	defer rows.Close()

	sr := &data.PRSizeSeries{
		Labels: make([]string, 0),
		Small:  make([]int, 0),
		Medium: make([]int, 0),
		Large:  make([]int, 0),
		XLarge: make([]int, 0),
	}

	for rows.Next() {
		var label string
		var small, medium, large, xlarge int
		if err := rows.Scan(&label, &small, &medium, &large, &xlarge); err != nil {
			return nil, fmt.Errorf("failed to scan PR size row: %w", err)
		}
		sr.Labels = append(sr.Labels, label)
		sr.Small = append(sr.Small, small)
		sr.Medium = append(sr.Medium, medium)
		sr.Large = append(sr.Large, large)
		sr.XLarge = append(sr.XLarge, xlarge)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating rows: %w", err)
	}

	gf := newGapFiller(days, sr.Labels)
	sr.Labels = gf.periods
	sr.Small = gf.fillInt(sr.Small)
	sr.Medium = gf.fillInt(sr.Medium)
	sr.Large = gf.fillInt(sr.Large)
	sr.XLarge = gf.fillInt(sr.XLarge)

	return sr, nil
}

func (s *Store) GetForksAndActivity(ctx context.Context, org, repo, entity *string, days int) (*data.ForksAndActivitySeries, error) {
	if s.db == nil {
		return nil, data.ErrDBNotInitialized
	}

	gran := AutoGranularity(days)
	since := sinceDate(days)
	qb := newQueryBuilder(2)
	qb.addOptional("e.org", org)
	qb.addOptional("e.repo", repo)
	qb.addOptional("d.entity", entity)
	query := fmt.Sprintf(selectForksAndActivityTpl, GroupExpr(gran, "e.date"), qb.whereClause())
	args := append([]any{since}, qb.args...)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query forks and activity: %w", err)
	}
	defer rows.Close()

	sr := &data.ForksAndActivitySeries{
		Labels: make([]string, 0),
		Forks:  make([]int, 0),
		Events: make([]int, 0),
	}

	for rows.Next() {
		var label string
		var forks, events int
		if err := rows.Scan(&label, &forks, &events); err != nil {
			return nil, fmt.Errorf("failed to scan forks and activity row: %w", err)
		}
		sr.Labels = append(sr.Labels, label)
		sr.Forks = append(sr.Forks, forks)
		sr.Events = append(sr.Events, events)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating rows: %w", err)
	}

	gf := newGapFiller(days, sr.Labels)
	sr.Labels = gf.periods
	sr.Forks = gf.fillInt(sr.Forks)
	sr.Events = gf.fillInt(sr.Events)

	return sr, nil
}

func (s *Store) GetContributorFunnel(ctx context.Context, org, repo, entity *string, days int) (*data.ContributorFunnelSeries, error) {
	if s.db == nil {
		return nil, data.ErrDBNotInitialized
	}

	gran := AutoGranularity(days)
	since := sinceDate(days)
	qb := newQueryBuilder(2)
	qb.addOptional("e.org", org)
	qb.addOptional("e.repo", repo)
	qb.addOptional("d.entity", entity)
	query := fmt.Sprintf(selectContributorFunnelTpl,
		GroupExpr(gran, "date"),
		GroupExpr(gran, "f.first_comment"),
		GroupExpr(gran, "f.first_pr"),
		GroupExpr(gran, "f.first_merge"),
		qb.whereClause(),
	)
	args := append([]any{since}, qb.args...)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query contributor funnel: %w", err)
	}
	defer rows.Close()

	sr := &data.ContributorFunnelSeries{
		Labels:       make([]string, 0),
		FirstComment: make([]int, 0),
		FirstPR:      make([]int, 0),
		FirstMerge:   make([]int, 0),
	}

	for rows.Next() {
		var label string
		var fc, fp, fm int
		if err := rows.Scan(&label, &fc, &fp, &fm); err != nil {
			return nil, fmt.Errorf("failed to scan contributor funnel row: %w", err)
		}
		sr.Labels = append(sr.Labels, label)
		sr.FirstComment = append(sr.FirstComment, fc)
		sr.FirstPR = append(sr.FirstPR, fp)
		sr.FirstMerge = append(sr.FirstMerge, fm)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating rows: %w", err)
	}

	gf := newGapFiller(days, sr.Labels)
	sr.Labels = gf.periods
	sr.FirstComment = gf.fillInt(sr.FirstComment)
	sr.FirstPR = gf.fillInt(sr.FirstPR)
	sr.FirstMerge = gf.fillInt(sr.FirstMerge)

	return sr, nil
}

func (s *Store) GetContributorMomentum(ctx context.Context, org, repo, entity *string, days int) (*data.MomentumSeries, error) {
	if s.db == nil {
		return nil, data.ErrDBNotInitialized
	}

	gran := AutoGranularity(days)
	since := sinceDate(days)
	qb := newQueryBuilder(2)
	qb.addOptional("e.org", org)
	qb.addOptional("e.repo", repo)
	qb.addOptional("d.entity", entity)
	query := fmt.Sprintf(selectContributorMomentumTpl, GroupExpr(gran, "date"), MomentumInterval(gran), MomentumFormat(gran), qb.whereClause())
	args := append([]any{since}, qb.args...)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query contributor momentum: %w", err)
	}
	defer rows.Close()

	sr := &data.MomentumSeries{
		Labels: make([]string, 0),
		Active: make([]int, 0),
		Delta:  make([]int, 0),
	}

	for rows.Next() {
		var label string
		var active int
		if err := rows.Scan(&label, &active); err != nil {
			return nil, fmt.Errorf("failed to scan contributor momentum row: %w", err)
		}
		sr.Labels = append(sr.Labels, label)
		sr.Active = append(sr.Active, active)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating rows: %w", err)
	}

	gf := newGapFiller(days, sr.Labels)
	sr.Labels = gf.periods
	sr.Active = gf.fillInt(sr.Active)
	sr.Delta = make([]int, len(sr.Active))
	for i := range sr.Active {
		if i == 0 {
			sr.Delta[i] = 0
		} else {
			sr.Delta[i] = sr.Active[i] - sr.Active[i-1]
		}
	}

	return sr, nil
}

func (s *Store) GetContributorProfile(ctx context.Context, username string, org, repo, entity *string, days int) (*data.ContributorProfileSeries, error) {
	if s.db == nil {
		return nil, data.ErrDBNotInitialized
	}

	if username == "" {
		return nil, fmt.Errorf("username is required")
	}

	since := sinceDate(days)
	qb := newQueryBuilder(3)
	qb.addOptional("e.org", org)
	qb.addOptional("e.repo", repo)
	qb.addOptional("d.entity", entity)
	query := fmt.Sprintf(selectContributorProfileTpl, qb.whereClause())
	args := append([]any{username, since}, qb.args...)

	var prs, prsMerged, reviews, issues, comments int
	var prSmall, prMedium, prLarge, prXLarge int
	var medPrs, medMerged, medReviews, medIssues, medComments float64
	var medSmall, medMedium, medLarge, medXLarge float64

	err := s.db.QueryRowContext(ctx, query, args...).Scan(
		&prs, &prsMerged, &reviews, &issues, &comments,
		&prSmall, &prMedium, &prLarge, &prXLarge,
		&medPrs, &medMerged, &medReviews, &medIssues, &medComments,
		&medSmall, &medMedium, &medLarge, &medXLarge,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query contributor profile: %w", err)
	}

	result := &data.ContributorProfileSeries{
		Metrics: []string{"PRs Opened", "PRs Merged", "PR Reviews", "Issues Opened", "Issue Comments",
			"PR Size S", "PR Size M", "PR Size L", "PR Size XL"},
		Values:  []int{prs, prsMerged, reviews, issues, comments, prSmall, prMedium, prLarge, prXLarge},
		Medians: []float64{medPrs, medMerged, medReviews, medIssues, medComments, medSmall, medMedium, medLarge, medXLarge},
	}

	var rep sql.NullFloat64
	if scanErr := s.db.QueryRowContext(ctx, `SELECT reputation FROM devpulse_developer WHERE username = $1`, username).Scan(&rep); scanErr == nil && rep.Valid {
		result.Reputation = &rep.Float64
	}

	return result, nil
}

func (s *Store) GetTimeToFirstResponse(ctx context.Context, org, repo, entity *string, days int) (*data.FirstResponseSeries, error) {
	ms, issueAvg, prAvg, err := getDualSeries[float64](ctx, s.db, selectTimeToFirstResponseTpl, []string{"e.created_at"}, org, repo, entity, days)
	if err != nil {
		return nil, err
	}
	return &data.FirstResponseSeries{Labels: ms, IssueAvg: issueAvg, PRAvg: prAvg}, nil
}

func (s *Store) GetAgingPRs(ctx context.Context, org, repo, entity *string, days int) (*data.AgingPRsSeries, error) {
	if s.db == nil {
		return nil, data.ErrDBNotInitialized
	}

	since := sinceDate(days)
	qb := newQueryBuilder(2)
	qb.addOptional("e.org", org)
	qb.addOptional("e.repo", repo)
	qb.addOptional("d.entity", entity)
	query := fmt.Sprintf(selectAgingPRsTpl, qb.whereClause())
	args := append([]any{since}, qb.args...)

	var total, over30, over90 int

	if err := s.db.QueryRowContext(ctx, query, args...).Scan(&total, &over30, &over90); err != nil {
		return nil, fmt.Errorf("failed to query aging PRs: %w", err)
	}

	var pct float64
	if total > 0 {
		pct = float64(over30) / float64(total) * 100
	}

	return &data.AgingPRsSeries{
		TotalOpen:  total,
		Over30Days: over30,
		Over90Days: over90,
		AgingPct:   pct,
	}, nil
}

func (s *Store) GetIssueOpenCloseRatio(ctx context.Context, org, repo, entity *string, days int) (*data.IssueRatioSeries, error) {
	ms, opened, closed, err := getDualSeries[int](ctx, s.db, selectIssueOpenCloseRatioTpl, []string{"e.created_at", "e.closed_at"}, org, repo, entity, days)
	if err != nil {
		return nil, err
	}
	return &data.IssueRatioSeries{Labels: ms, Opened: opened, Closed: closed}, nil
}

func (s *Store) GetUnansweredRate(ctx context.Context, org, repo, entity *string, days int) (*data.UnansweredSeries, error) {
	if s.db == nil {
		return nil, data.ErrDBNotInitialized
	}

	since := sinceDate(days)
	qb := newQueryBuilder(2)
	qb.addOptional("e.org", org)
	qb.addOptional("e.repo", repo)
	qb.addOptional("d.entity", entity)
	query := fmt.Sprintf(selectUnansweredRateTpl, qb.whereClause())
	args := append([]any{since}, qb.args...)

	var total, unanswered int

	if err := s.db.QueryRowContext(ctx, query, args...).Scan(&total, &unanswered); err != nil {
		return nil, fmt.Errorf("failed to query unanswered rate: %w", err)
	}

	var pct float64
	if total > 0 {
		pct = float64(unanswered) / float64(total) * 100
	}

	return &data.UnansweredSeries{
		TotalItems:    total,
		Unanswered:    unanswered,
		UnansweredPct: pct,
	}, nil
}

func (s *Store) GetResponseSLO(ctx context.Context, org, repo, entity *string, days int) (*data.ResponseSLOSeries, error) {
	if s.db == nil {
		return nil, data.ErrDBNotInitialized
	}

	since := sinceDate(days)
	qb := newQueryBuilder(2)
	qb.addOptional("e.org", org)
	qb.addOptional("e.repo", repo)
	qb.addOptional("d.entity", entity)
	query := fmt.Sprintf(selectResponseSLOTpl, qb.whereClause())
	args := append([]any{since}, qb.args...)

	var total, withinSLO int

	if err := s.db.QueryRowContext(ctx, query, args...).Scan(&total, &withinSLO); err != nil {
		return nil, fmt.Errorf("failed to query response SLO: %w", err)
	}

	var pct float64
	if total > 0 {
		pct = float64(withinSLO) / float64(total) * 100
	}

	return &data.ResponseSLOSeries{
		TotalItems:    total,
		WithinSLO:     withinSLO,
		WithinSLOPct:  pct,
		SLOThresholdH: 48,
	}, nil
}

func (s *Store) GetPortfolioSummary(ctx context.Context, org, repo *string, days int) (*data.PortfolioSummary, error) {
	if s.db == nil {
		return nil, data.ErrDBNotInitialized
	}

	since := sinceDate(days)
	thirtyDaysAgo := sinceDate(30)

	// Fixed params: $1=since, $2=30_days_ago. Dynamic org/repo start at $3.
	qbMeta := newQueryBuilder(3)
	qbMeta.addOptional("org", org)
	qbMeta.addOptional("repo", repo)

	qbHist := newQueryBuilder(qbMeta.nextParam())
	qbHist.addOptional("org", org)
	qbHist.addOptional("repo", repo)

	qbEvent := newQueryBuilder(qbHist.nextParam())
	qbEvent.addOptional("e.org", org)
	qbEvent.addOptional("e.repo", repo)

	query := fmt.Sprintf(selectPortfolioSummaryTpl,
		qbMeta.whereClause(), qbHist.whereClause(), qbEvent.whereClause())

	args := make([]any, 0, 2+len(qbMeta.args)+len(qbHist.args)+len(qbEvent.args))
	args = append(args, since, thirtyDaysAgo)
	args = append(args, qbMeta.args...)
	args = append(args, qbHist.args...)
	args = append(args, qbEvent.args...)

	var ps data.PortfolioSummary
	if err := s.db.QueryRowContext(ctx, query, args...).Scan(
		&ps.TotalStars, &ps.TotalForks, &ps.TotalOpenIssues,
		&ps.StarsDelta, &ps.ForksDelta,
		&ps.TotalClosedPRs, &ps.TotalContributors,
		&ps.AvgMergeHours, &ps.MedianMergeHours,
	); err != nil {
		return nil, fmt.Errorf("failed to query portfolio summary: %w", err)
	}

	prevStars := ps.TotalStars - ps.StarsDelta
	if prevStars > 0 {
		ps.StarsDeltaPct = float64(ps.StarsDelta) / float64(prevStars) * 100
	}
	prevForks := ps.TotalForks - ps.ForksDelta
	if prevForks > 0 {
		ps.ForksDeltaPct = float64(ps.ForksDelta) / float64(prevForks) * 100
	}

	return &ps, nil
}

func (s *Store) GetSignals(ctx context.Context, org *string, limit int) ([]*data.Signal, error) {
	if s.db == nil {
		return nil, data.ErrDBNotInitialized
	}

	if limit <= 0 {
		limit = 10
	}

	now := time.Now().UTC()
	weekAgo := now.AddDate(0, 0, -7).Format("2006-01-02")
	twoWeeksAgo := now.AddDate(0, 0, -14).Format("2006-01-02")

	// Fixed params: $1=weekAgo, $2=twoWeeksAgo, $3=limit. Dynamic org starts at $4.
	qbThis := newQueryBuilder(4)
	qbThis.addOptional("e.org", org)
	qbLast := newQueryBuilder(qbThis.nextParam())
	qbLast.addOptional("e.org", org)

	query := fmt.Sprintf(selectSignalsTpl, qbThis.whereClause(), qbLast.whereClause())
	args := make([]any, 0, 3+len(qbThis.args)+len(qbLast.args))
	args = append(args, weekAgo, twoWeeksAgo, limit)
	args = append(args, qbThis.args...)
	args = append(args, qbLast.args...)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query signals: %w", err)
	}
	defer rows.Close()

	var signals []*data.Signal
	for rows.Next() {
		var sig data.Signal
		var delta int
		var deltaPct float64
		if err := rows.Scan(&sig.Org, &sig.Repo, &delta, &deltaPct); err != nil {
			return nil, fmt.Errorf("failed to scan signal row: %w", err)
		}
		sig.Metric = "events"
		sig.Delta = delta
		sig.DeltaPct = deltaPct

		absPct := deltaPct
		if absPct < 0 {
			absPct = -absPct
		}

		switch {
		case absPct >= 200:
			sig.Severity = "critical"
		case absPct >= 50:
			sig.Severity = "warning"
		default:
			sig.Severity = "info"
		}

		if delta > 0 {
			sig.Message = fmt.Sprintf("+%d events WoW (+%.0f%%)", delta, deltaPct)
		} else {
			sig.Message = fmt.Sprintf("%d events WoW (%.0f%%)", delta, deltaPct)
		}

		signals = append(signals, &sig)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating signal rows: %w", err)
	}

	return signals, nil
}
