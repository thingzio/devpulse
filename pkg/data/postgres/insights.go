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
	// selectBusFactorSQL: $1=org, $2=repo, $3=entity, $4=since
	selectBusFactorSQL = `WITH dev_counts AS (
			SELECT e.username, COUNT(*) AS cnt
			FROM event e
			JOIN developer d ON e.username = d.username
			WHERE e.org = COALESCE($1, e.org)
			  AND e.repo = COALESCE($2, e.repo)
			  AND COALESCE(d.entity, '') = COALESCE($3, COALESCE(d.entity, ''))
			  AND e.date >= $4
			  ` + botExcludeSQL + `
			  ` + forkExcludeSQL + `
			GROUP BY e.username
			ORDER BY cnt DESC
		),
		running AS (
			SELECT username, cnt,
				SUM(cnt) OVER (ORDER BY cnt DESC) AS cumsum,
				SUM(cnt) OVER () AS total
			FROM dev_counts
		)
		SELECT COUNT(*) FROM running WHERE cumsum - cnt < total * 0.5
	`

	// selectPonyFactorSQL: $1=org, $2=repo, $3=entity, $4=since
	selectPonyFactorSQL = `WITH ent_counts AS (
			SELECT d.entity, COUNT(*) AS cnt
			FROM event e
			JOIN developer d ON e.username = d.username
			WHERE e.org = COALESCE($1, e.org)
			  AND e.repo = COALESCE($2, e.repo)
			  AND COALESCE(d.entity, '') = COALESCE($3, COALESCE(d.entity, ''))
			  AND e.date >= $4
			  AND d.entity IS NOT NULL AND d.entity != ''
			  ` + forkExcludeSQL + `
			GROUP BY d.entity
			ORDER BY cnt DESC
		),
		running AS (
			SELECT entity, cnt,
				SUM(cnt) OVER (ORDER BY cnt DESC) AS cumsum,
				SUM(cnt) OVER () AS total
			FROM ent_counts
		)
		SELECT COUNT(*) FROM running WHERE cumsum - cnt < total * 0.5
	`

	// selectRetentionTpl: $1=org, $2=repo, $3=entity, $4=since, $5=org, $6=repo, $7=entity, $8=since
	// %[1]s = GroupExpr(gran, "e.date")
	selectRetentionTpl = `WITH first_seen AS (
			SELECT e.username, MIN(%[1]s) AS first_period
			FROM event e
			JOIN developer d ON e.username = d.username
			WHERE e.org = COALESCE($1, e.org)
			  AND e.repo = COALESCE($2, e.repo)
			  AND COALESCE(d.entity, '') = COALESCE($3, COALESCE(d.entity, ''))
			  AND e.date >= $4
			  ` + botExcludeTpl + `
			  ` + forkExcludeSQL + `
			GROUP BY e.username
		),
		periods AS (
			SELECT DISTINCT e.username, %[1]s AS period
			FROM event e
			JOIN developer d ON e.username = d.username
			WHERE e.org = COALESCE($5, e.org)
			  AND e.repo = COALESCE($6, e.repo)
			  AND COALESCE(d.entity, '') = COALESCE($7, COALESCE(d.entity, ''))
			  AND e.date >= $8
			  ` + botExcludeTpl + `
			  ` + forkExcludeSQL + `
		)
		SELECT p.period,
			SUM(CASE WHEN f.first_period = p.period THEN 1 ELSE 0 END) AS new_contributors,
			SUM(CASE WHEN f.first_period < p.period THEN 1 ELSE 0 END) AS returning_contributors
		FROM periods p
		JOIN first_seen f ON p.username = f.username
		GROUP BY p.period
		ORDER BY p.period
	`

	// selectTimeToMergeTpl: $1=org, $2=repo, $3=entity, $4=since
	// %s = GroupExpr(gran, "e.created_at")
	selectTimeToMergeTpl = `SELECT
			%s AS period,
			COUNT(*) AS cnt,
			AVG(EXTRACT(EPOCH FROM (e.merged_at::timestamp - e.created_at::timestamp)) / 86400.0) AS avg_days
		FROM event e
		JOIN developer d ON e.username = d.username
		WHERE e.type = 'pr'
		  AND e.merged_at IS NOT NULL
		  AND e.created_at IS NOT NULL
		  AND e.org = COALESCE($1, e.org)
		  AND e.repo = COALESCE($2, e.repo)
		  AND COALESCE(d.entity, '') = COALESCE($3, COALESCE(d.entity, ''))
		  AND e.created_at >= $4
		  ` + botExcludeTpl + `
		GROUP BY period
		ORDER BY period
	`

	// selectTimeToRestoreBugsTpl: $1=org, $2=repo, $3=entity, $4=since
	// %s = GroupExpr(gran, "e.created_at")
	selectTimeToRestoreBugsTpl = `SELECT
			%s AS period,
			COUNT(*) AS cnt,
			AVG(EXTRACT(EPOCH FROM (e.closed_at::timestamp - e.created_at::timestamp)) / 86400.0) AS avg_days
		FROM event e
		JOIN developer d ON e.username = d.username
		WHERE e.type = 'issue'
		  AND e.closed_at IS NOT NULL
		  AND e.created_at IS NOT NULL
		  AND e.state = 'closed'
		  AND LOWER(e.labels) LIKE '%%bug%%'
		  AND EXISTS (
		      SELECT 1 FROM release r
		      WHERE r.org = e.org AND r.repo = e.repo
		        AND EXTRACT(EPOCH FROM (e.created_at::timestamp - r.published_at::timestamp)) / 86400.0 BETWEEN 0 AND 7
		  )
		  AND e.org = COALESCE($1, e.org)
		  AND e.repo = COALESCE($2, e.repo)
		  AND COALESCE(d.entity, '') = COALESCE($3, COALESCE(d.entity, ''))
		  AND e.created_at >= $4
		  ` + botExcludeTpl + `
		GROUP BY period
		ORDER BY period
	`

	// selectTimeToCloseTpl: $1=org, $2=repo, $3=entity, $4=since
	// %s = GroupExpr(gran, "e.created_at")
	selectTimeToCloseTpl = `SELECT
			%s AS period,
			COUNT(*) AS cnt,
			AVG(EXTRACT(EPOCH FROM (e.closed_at::timestamp - e.created_at::timestamp)) / 86400.0) AS avg_days
		FROM event e
		JOIN developer d ON e.username = d.username
		WHERE e.type = 'issue'
		  AND e.closed_at IS NOT NULL
		  AND e.created_at IS NOT NULL
		  AND e.state = 'closed'
		  AND e.org = COALESCE($1, e.org)
		  AND e.repo = COALESCE($2, e.repo)
		  AND COALESCE(d.entity, '') = COALESCE($3, COALESCE(d.entity, ''))
		  AND e.created_at >= $4
		  ` + botExcludeTpl + `
		GROUP BY period
		ORDER BY period
	`

	// selectForksAndActivityTpl: $1=org, $2=repo, $3=entity, $4=since
	// %s = GroupExpr(gran, "e.date")
	selectForksAndActivityTpl = `SELECT
			%s AS period,
			SUM(CASE WHEN e.type = 'fork' THEN 1 ELSE 0 END) AS forks,
			COUNT(*) AS events
		FROM event e
		JOIN developer d ON e.username = d.username
		WHERE e.org = COALESCE($1, e.org)
		  AND e.repo = COALESCE($2, e.repo)
		  AND COALESCE(d.entity, '') = COALESCE($3, COALESCE(d.entity, ''))
		  AND e.date >= $4
		  ` + botExcludeTpl + `
		GROUP BY period
		ORDER BY period
	`

	// selectPRReviewRatioTpl: $1=pr_type, $2=review_type, $3=org, $4=repo, $5=entity, $6=since, $7=pr_type, $8=review_type
	// %s = GroupExpr(gran, "e.date")
	selectPRReviewRatioTpl = `SELECT
			%s AS period,
			SUM(CASE WHEN e.type = $1 THEN 1 ELSE 0 END) AS prs,
			SUM(CASE WHEN e.type = $2 THEN 1 ELSE 0 END) AS reviews
		FROM event e
		JOIN developer d ON e.username = d.username
		WHERE e.org = COALESCE($3, e.org)
		  AND e.repo = COALESCE($4, e.repo)
		  AND COALESCE(d.entity, '') = COALESCE($5, COALESCE(d.entity, ''))
		  AND e.date >= $6
		  AND e.type IN ($7, $8)
		  ` + botExcludeTpl + `
		GROUP BY period
		ORDER BY period
	`

	// selectChangeFailuresTpl: $1=org, $2=repo, $3=entity, $4=since
	// %s = GroupExpr(gran, "e.created_at")
	selectChangeFailuresTpl = `SELECT
		%s AS period,
		COUNT(*) AS failures
	FROM event e
	JOIN developer d ON e.username = d.username
	WHERE (
	    (e.type = 'issue' AND LOWER(e.labels) LIKE '%%bug%%'
	     AND EXISTS (
	        SELECT 1 FROM release r
	        WHERE r.org = e.org AND r.repo = e.repo
	          AND EXTRACT(EPOCH FROM (e.created_at::timestamp - r.published_at::timestamp)) / 86400.0 BETWEEN 0 AND 7
	     ))
	    OR
	    (e.type = 'pr' AND LOWER(e.title) LIKE '%%revert%%')
	)
	  AND e.org = COALESCE($1, e.org)
	  AND e.repo = COALESCE($2, e.repo)
	  AND COALESCE(d.entity, '') = COALESCE($3, COALESCE(d.entity, ''))
	  AND e.created_at >= $4
	  ` + botExcludeTpl + `
	GROUP BY period
	ORDER BY period
	`

	// selectDeploymentCountTpl: $1=org, $2=repo, $3=since
	// %s = GroupExpr(gran, "published_at")
	selectDeploymentCountTpl = `SELECT
		%s AS period,
		COUNT(*) AS cnt
	FROM release
	WHERE org = COALESCE($1, org)
	  AND repo = COALESCE($2, repo)
	  AND published_at >= $3
	GROUP BY period
	ORDER BY period
	`

	// selectReviewLatencyTpl: $1=since, $2=org, $3=repo, $4=entity, $5=since
	// %[1]s = GroupExpr(gran, "date"), %[2]s = GroupExpr(gran, "pr.created_at")
	selectReviewLatencyTpl = `WITH periods AS (
		SELECT DISTINCT %[1]s AS period
		FROM event
		WHERE date >= $1
	),
	latency AS (
		SELECT
			%[2]s AS period,
			(EXTRACT(EPOCH FROM (MIN(rev.created_at::timestamp) - MIN(pr.created_at::timestamp))) / 3600.0) AS hours
		FROM event pr
		JOIN event rev ON pr.org = rev.org AND pr.repo = rev.repo AND pr.number = rev.number
			AND rev.type = 'pr_review'
		JOIN developer d ON pr.username = d.username
		WHERE pr.type = 'pr'
		  AND pr.number IS NOT NULL
		  AND pr.created_at IS NOT NULL
		  AND rev.created_at IS NOT NULL
		  AND pr.org = COALESCE($2, pr.org)
		  AND pr.repo = COALESCE($3, pr.repo)
		  AND COALESCE(d.entity, '') = COALESCE($4, COALESCE(d.entity, ''))
		  AND pr.created_at >= $5
		  ` + botExcludePrTpl + `
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

	// selectPRSizeDistributionTpl: $1=org, $2=repo, $3=entity, $4=since
	// %s = GroupExpr(gran, "e.created_at")
	selectPRSizeDistributionTpl = `SELECT
		%s AS period,
		SUM(CASE WHEN COALESCE(e.additions, 0) + COALESCE(e.deletions, 0) < 50 THEN 1 ELSE 0 END) AS small,
		SUM(CASE WHEN COALESCE(e.additions, 0) + COALESCE(e.deletions, 0) BETWEEN 50 AND 249 THEN 1 ELSE 0 END) AS medium,
		SUM(CASE WHEN COALESCE(e.additions, 0) + COALESCE(e.deletions, 0) BETWEEN 250 AND 999 THEN 1 ELSE 0 END) AS large,
		SUM(CASE WHEN COALESCE(e.additions, 0) + COALESCE(e.deletions, 0) >= 1000 THEN 1 ELSE 0 END) AS xlarge
	FROM event e
	JOIN developer d ON e.username = d.username
	WHERE e.type = 'pr'
	  AND e.created_at IS NOT NULL
	  AND e.org = COALESCE($1, e.org)
	  AND e.repo = COALESCE($2, e.repo)
	  AND COALESCE(d.entity, '') = COALESCE($3, COALESCE(d.entity, ''))
	  AND e.created_at >= $4
	  ` + botExcludeTpl + `
	GROUP BY period
	ORDER BY period
	`

	// selectContributorMomentumTpl: $1=since, $2=org, $3=repo, $4=entity
	// %[1]s = GroupExpr(gran, "date"), %[2]s = MomentumInterval(gran), %[3]s = MomentumFormat(gran)
	selectContributorMomentumTpl = `WITH periods AS (
		SELECT DISTINCT %[1]s AS period
		FROM event
		WHERE date >= $1
	)
	SELECT
		p.period,
		COUNT(DISTINCT e.username) AS active
	FROM periods p
	JOIN event e ON %[1]s >= TO_CHAR(((CASE WHEN length(p.period) = 7 THEN p.period || '-01' ELSE p.period END)::date - INTERVAL '%[2]s'), %[3]s)
		AND %[1]s <= p.period
	JOIN developer d ON e.username = d.username
	WHERE e.org = COALESCE($2, e.org)
	  AND e.repo = COALESCE($3, e.repo)
	  AND COALESCE(d.entity, '') = COALESCE($4, COALESCE(d.entity, ''))
	  ` + botExcludeTpl + `
	  ` + forkExcludeSQL + `
	GROUP BY p.period
	ORDER BY p.period
	`

	// selectContributorFunnelTpl: $1=org, $2=repo, $3=entity, $4=since
	// %[1]s = GroupExpr(gran, "date")
	// %[2]s = GroupExpr(gran, "f.first_comment")
	// %[3]s = GroupExpr(gran, "f.first_pr")
	// %[4]s = GroupExpr(gran, "f.first_merge")
	// %[5]s = GroupExpr(gran, "COALESCE(f.first_comment, f.first_pr, f.first_merge)")
	selectContributorFunnelTpl = `WITH firsts AS (
		SELECT
			e.username,
			MIN(CASE WHEN e.type = 'issue_comment' THEN e.date END) AS first_comment,
			MIN(CASE WHEN e.type = 'pr' THEN e.date END) AS first_pr,
			MIN(CASE WHEN e.type = 'pr' AND e.state = 'merged' THEN e.date END) AS first_merge
		FROM event e
		JOIN developer d ON e.username = d.username
		WHERE e.org = COALESCE($1, e.org)
		  AND e.repo = COALESCE($2, e.repo)
		  AND COALESCE(d.entity, '') = COALESCE($3, COALESCE(d.entity, ''))
		  ` + botExcludeTpl + `
		GROUP BY e.username
	),
	periods AS (
		SELECT DISTINCT %[1]s AS period FROM event WHERE date >= $4
	)
	SELECT
		p.period,
		SUM(CASE WHEN f.first_comment IS NOT NULL AND %[2]s = p.period THEN 1 ELSE 0 END) AS fc,
		SUM(CASE WHEN f.first_pr IS NOT NULL AND %[3]s = p.period THEN 1 ELSE 0 END) AS fp,
		SUM(CASE WHEN f.first_merge IS NOT NULL AND %[4]s = p.period THEN 1 ELSE 0 END) AS fm
	FROM periods p
	CROSS JOIN firsts f
	WHERE %[5]s >= (SELECT MIN(period) FROM periods)
	GROUP BY p.period
	HAVING SUM(CASE WHEN f.first_comment IS NOT NULL AND %[2]s = p.period THEN 1 ELSE 0 END) > 0
	    OR SUM(CASE WHEN f.first_pr IS NOT NULL AND %[3]s = p.period THEN 1 ELSE 0 END) > 0
	    OR SUM(CASE WHEN f.first_merge IS NOT NULL AND %[4]s = p.period THEN 1 ELSE 0 END) > 0
	ORDER BY p.period
	`

	// selectContributorProfileSQL:
	// user_counts: $1=username, $2=org, $3=repo, $4=entity, $5=since
	// avg_counts:  $6=org, $7=repo, $8=entity, $9=since
	selectContributorProfileSQL = `WITH user_counts AS (
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
		FROM event e
		JOIN developer d ON e.username = d.username
		WHERE e.username = $1
		  AND e.org = COALESCE($2, e.org)
		  AND e.repo = COALESCE($3, e.repo)
		  AND COALESCE(d.entity, '') = COALESCE($4, COALESCE(d.entity, ''))
		  AND e.date >= $5
	),
	avg_counts AS (
		SELECT
			AVG(prs_opened) AS prs_opened,
			AVG(prs_merged) AS prs_merged,
			AVG(pr_reviews) AS pr_reviews,
			AVG(issues_opened) AS issues_opened,
			AVG(issue_comments) AS issue_comments,
			AVG(pr_small) AS pr_small,
			AVG(pr_medium) AS pr_medium,
			AVG(pr_large) AS pr_large,
			AVG(pr_xlarge) AS pr_xlarge
		FROM (
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
			FROM event e
			JOIN developer d ON e.username = d.username
			WHERE e.org = COALESCE($6, e.org)
			  AND e.repo = COALESCE($7, e.repo)
			  AND COALESCE(d.entity, '') = COALESCE($8, COALESCE(d.entity, ''))
			  AND e.date >= $9
			  ` + botExcludeSQL + `
			GROUP BY e.username
		) sub
	)
	SELECT
		COALESCE(u.prs_opened, 0), COALESCE(u.prs_merged, 0), COALESCE(u.pr_reviews, 0),
		COALESCE(u.issues_opened, 0), COALESCE(u.issue_comments, 0),
		COALESCE(u.pr_small, 0), COALESCE(u.pr_medium, 0), COALESCE(u.pr_large, 0), COALESCE(u.pr_xlarge, 0),
		COALESCE(a.prs_opened, 0), COALESCE(a.prs_merged, 0), COALESCE(a.pr_reviews, 0),
		COALESCE(a.issues_opened, 0), COALESCE(a.issue_comments, 0),
		COALESCE(a.pr_small, 0), COALESCE(a.pr_medium, 0), COALESCE(a.pr_large, 0), COALESCE(a.pr_xlarge, 0)
	FROM user_counts u, avg_counts a
	`

	// selectBannerStatsSQL: $1=org, $2=repo, $3=entity, $4=since
	selectBannerStatsSQL = `SELECT
		COUNT(DISTINCT e.org),
		COUNT(DISTINCT e.org || '/' || e.repo),
		COUNT(*),
		COUNT(DISTINCT e.username),
		COALESCE((SELECT MAX(rm.last_import_at) FROM repo_meta rm
			WHERE rm.org = COALESCE($1, rm.org) AND rm.repo = COALESCE($2, rm.repo)), '')
	FROM event e
	WHERE e.org = COALESCE($1, e.org)
	  AND e.repo = COALESCE($2, e.repo)
	  AND COALESCE((SELECT d.entity FROM developer d WHERE d.username = e.username), '') = COALESCE($3, COALESCE((SELECT d.entity FROM developer d WHERE d.username = e.username), ''))
	  AND e.date >= $4
	  ` + botExcludeSQL + `
	  ` + forkExcludeSQL + `
	`

	// selectIssueOpenCloseRatioTpl: $1=org, $2=repo, $3=entity, $4=since, $5=org, $6=repo, $7=entity, $8=since
	// %[1]s = GroupExpr(gran, "e.created_at"), %[2]s = GroupExpr(gran, "e.closed_at")
	selectIssueOpenCloseRatioTpl = `SELECT period, SUM(opened) AS opened, SUM(closed) AS closed
		FROM (
			SELECT %[1]s AS period, 1 AS opened, 0 AS closed
			FROM event e
			JOIN developer d ON e.username = d.username
			WHERE e.type = 'issue'
			  AND e.created_at IS NOT NULL
			  AND e.org = COALESCE($1, e.org)
			  AND e.repo = COALESCE($2, e.repo)
			  AND COALESCE(d.entity, '') = COALESCE($3, COALESCE(d.entity, ''))
			  AND e.created_at >= $4
			  ` + botExcludeTpl + `
			UNION ALL
			SELECT %[2]s AS period, 0 AS opened, 1 AS closed
			FROM event e
			JOIN developer d ON e.username = d.username
			WHERE e.type = 'issue'
			  AND e.closed_at IS NOT NULL
			  AND e.org = COALESCE($5, e.org)
			  AND e.repo = COALESCE($6, e.repo)
			  AND COALESCE(d.entity, '') = COALESCE($7, COALESCE(d.entity, ''))
			  AND e.closed_at >= $8
			  ` + botExcludeTpl + `
		) sub
		GROUP BY period
		ORDER BY period
	`

	// selectTimeToFirstResponseTpl: $1=org, $2=repo, $3=entity, $4=since, $5=org, $6=repo, $7=entity, $8=since
	// %[1]s = GroupExpr(gran, "e.created_at")
	selectTimeToFirstResponseTpl = `WITH issue_first AS (
		SELECT
			e.org, e.repo, e.number,
			%[1]s AS period,
			MIN(
				EXTRACT(EPOCH FROM (c.created_at::timestamp - e.created_at::timestamp)) / 3600.0
			) AS hours_to_first
		FROM event e
		JOIN event c ON c.org = e.org AND c.repo = e.repo AND c.number = e.number
			AND c.type = 'issue_comment' AND c.created_at > e.created_at
		JOIN developer d ON e.username = d.username
		WHERE e.type = 'issue'
		  AND e.created_at IS NOT NULL
		  AND e.number IS NOT NULL
		  AND e.org = COALESCE($1, e.org)
		  AND e.repo = COALESCE($2, e.repo)
		  AND COALESCE(d.entity, '') = COALESCE($3, COALESCE(d.entity, ''))
		  AND e.created_at >= $4
		  ` + botExcludeTpl + `
		GROUP BY e.org, e.repo, e.number, period
	), pr_first AS (
		SELECT
			e.org, e.repo, e.number,
			%[1]s AS period,
			MIN(
				EXTRACT(EPOCH FROM (c.created_at::timestamp - e.created_at::timestamp)) / 3600.0
			) AS hours_to_first
		FROM event e
		JOIN event c ON c.org = e.org AND c.repo = e.repo AND c.number = e.number
			AND c.type = 'pr_review' AND c.created_at > e.created_at
		JOIN developer d ON e.username = d.username
		WHERE e.type = 'pr'
		  AND e.created_at IS NOT NULL
		  AND e.number IS NOT NULL
		  AND e.org = COALESCE($5, e.org)
		  AND e.repo = COALESCE($6, e.repo)
		  AND COALESCE(d.entity, '') = COALESCE($7, COALESCE(d.entity, ''))
		  AND e.created_at >= $8
		  ` + botExcludeTpl + `
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

	// selectAgingPRsSQL: $1=org, $2=repo, $3=entity, $4=since
	selectAgingPRsSQL = `SELECT
		COUNT(*) AS total_open,
		COALESCE(SUM(CASE WHEN EXTRACT(EPOCH FROM (NOW() - e.created_at::timestamp)) / 86400.0 > 30 THEN 1 ELSE 0 END), 0) AS over_30,
		COALESCE(SUM(CASE WHEN EXTRACT(EPOCH FROM (NOW() - e.created_at::timestamp)) / 86400.0 > 90 THEN 1 ELSE 0 END), 0) AS over_90
	FROM event e
	JOIN developer d ON e.username = d.username
	WHERE e.type = 'pr'
	  AND (e.state IS NULL OR e.state NOT IN ('merged', 'closed'))
	  AND e.created_at IS NOT NULL
	  AND e.org = COALESCE($1, e.org)
	  AND e.repo = COALESCE($2, e.repo)
	  AND COALESCE(d.entity, '') = COALESCE($3, COALESCE(d.entity, ''))
	  AND e.created_at >= $4
	  ` + botExcludeSQL + `
	`

	// selectUnansweredRateSQL: $1=org, $2=repo, $3=entity, $4=since
	selectUnansweredRateSQL = `WITH items AS (
    SELECT e.org, e.repo, e.number, e.type, e.username, e.created_at
    FROM event e
    JOIN developer d ON e.username = d.username
    WHERE e.type IN ('issue', 'pr')
      AND e.number IS NOT NULL
      AND e.created_at IS NOT NULL
      AND EXTRACT(EPOCH FROM (NOW() - e.created_at::timestamp)) / 86400.0 > 7
      AND e.org = COALESCE($1, e.org)
      AND e.repo = COALESCE($2, e.repo)
      AND COALESCE(d.entity, '') = COALESCE($3, COALESCE(d.entity, ''))
      AND e.created_at >= $4
      ` + botExcludeSQL + `
),
responded AS (
    SELECT DISTINCT i.org, i.repo, i.number
    FROM items i
    JOIN event r ON r.org = i.org AND r.repo = i.repo AND r.number = i.number
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

	// selectResponseSLOSQL: $1=org, $2=repo, $3=entity, $4=since
	selectResponseSLOSQL = `WITH first_response AS (
    SELECT e.org, e.repo, e.number,
        MIN(EXTRACT(EPOCH FROM (r.created_at::timestamp - e.created_at::timestamp)) / 3600.0) AS hours
    FROM event e
    JOIN event r ON r.org = e.org AND r.repo = e.repo AND r.number = e.number
        AND r.type IN ('issue_comment', 'pr_review')
        AND r.username != e.username
        AND r.created_at > e.created_at
    JOIN developer d ON e.username = d.username
    WHERE e.type IN ('issue', 'pr')
      AND e.number IS NOT NULL
      AND e.created_at IS NOT NULL
      AND e.org = COALESCE($1, e.org)
      AND e.repo = COALESCE($2, e.repo)
      AND COALESCE(d.entity, '') = COALESCE($3, COALESCE(d.entity, ''))
      AND e.created_at >= $4
      ` + botExcludeSQL + `
    GROUP BY e.org, e.repo, e.number
)
SELECT
    COUNT(*) AS total,
    COALESCE(SUM(CASE WHEN hours <= 48 THEN 1 ELSE 0 END), 0) AS within_slo
FROM first_response
`

	// selectPortfolioSummarySQL: $1=org, $2=repo, $3=since, $4=30_days_ago_date
	selectPortfolioSummarySQL = `WITH current_totals AS (
    SELECT
        COALESCE(SUM(stars), 0) AS stars,
        COALESCE(SUM(forks), 0) AS forks,
        COALESCE(SUM(open_issues), 0) AS open_issues
    FROM repo_meta
    WHERE org = COALESCE($1, org)
      AND repo = COALESCE($2, repo)
),
prev_snapshot AS (
    SELECT
        COALESCE(SUM(h.stars), 0) AS stars,
        COALESCE(SUM(h.forks), 0) AS forks
    FROM (
        SELECT DISTINCT ON (org, repo) org, repo, stars, forks
        FROM repo_metric_history
        WHERE org = COALESCE($1, org)
          AND repo = COALESCE($2, repo)
          AND date <= $4
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
    FROM event e
    WHERE e.type = 'pr'
      AND e.org = COALESCE($1, e.org)
      AND e.repo = COALESCE($2, e.repo)
      AND e.date >= $3
      ` + botExcludeSQL + `
)
SELECT c.stars, c.forks, c.open_issues,
       c.stars - p.stars, c.forks - p.forks,
       ps.closed_prs, ps.contributors,
       ps.avg_merge_h, ps.median_merge_h
FROM current_totals c, prev_snapshot p, pr_stats ps
`

	// selectSignalsSQL: $1=org, $2=7_days_ago, $3=14_days_ago, $4=limit
	selectSignalsSQL = `WITH this_week AS (
    SELECT e.org, e.repo, COUNT(*) AS events
    FROM event e
    WHERE e.org = COALESCE($1, e.org)
      AND e.date >= $2
      ` + botExcludeSQL + `
      ` + forkExcludeSQL + `
    GROUP BY e.org, e.repo
),
last_week AS (
    SELECT e.org, e.repo, COUNT(*) AS events
    FROM event e
    WHERE e.org = COALESCE($1, e.org)
      AND e.date >= $3 AND e.date < $2
      ` + botExcludeSQL + `
      ` + forkExcludeSQL + `
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
LIMIT $4
`

	// selectDailyActivitySQL: $1=org, $2=repo, $3=entity, $4=since
	selectDailyActivitySQL = `SELECT e.date, COUNT(*) AS cnt
		FROM event e
		JOIN developer d ON e.username = d.username
		WHERE e.org = COALESCE($1, e.org)
		  AND e.repo = COALESCE($2, e.repo)
		  AND COALESCE(d.entity, '') = COALESCE($3, COALESCE(d.entity, ''))
		  AND e.date >= $4
		  ` + botExcludeSQL + `
		  ` + forkExcludeSQL + `
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

	if err := s.db.QueryRowContext(ctx, selectBusFactorSQL, org, repo, entity, since).Scan(&summary.BusFactor); err != nil {
		return nil, fmt.Errorf("failed to query bus factor: %w", err)
	}

	if err := s.db.QueryRowContext(ctx, selectPonyFactorSQL, org, repo, entity, since).Scan(&summary.PonyFactor); err != nil {
		return nil, fmt.Errorf("failed to query pony factor: %w", err)
	}

	if err := s.db.QueryRowContext(ctx, selectBannerStatsSQL, org, repo, entity, since).Scan(
		&summary.Orgs, &summary.Repos, &summary.Events, &summary.Contributors, &summary.LastImport,
	); err != nil {
		return nil, fmt.Errorf("failed to query banner stats: %w", err)
	}

	return summary, nil
}

func (s *Store) GetDailyActivity(ctx context.Context, org, repo, entity *string, days int) (*data.DailyActivitySeries, error) {
	if s.db == nil {
		return nil, data.ErrDBNotInitialized
	}

	since := sinceDate(days)

	rows, err := s.db.QueryContext(ctx, selectDailyActivitySQL, org, repo, entity, since)
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
	fmtArgs := make([]any, len(cols))
	for i, col := range cols {
		fmtArgs[i] = GroupExpr(gran, col)
	}
	query := fmt.Sprintf(queryTpl, fmtArgs...)
	since := sinceDate(days)

	rows, err := db.QueryContext(ctx, query, org, repo, entity, since, org, repo, entity, since)
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
	query := fmt.Sprintf(selectPRReviewRatioTpl, GroupExpr(gran, "e.date"))
	since := sinceDate(days)

	rows, err := s.db.QueryContext(ctx, query,
		data.EventTypePR, data.EventTypePRReview,
		org, repo, entity, since,
		data.EventTypePR, data.EventTypePRReview)
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
	failQuery := fmt.Sprintf(selectChangeFailuresTpl, GroupExpr(gran, "e.created_at"))
	deployQuery := fmt.Sprintf(selectDeploymentCountTpl, GroupExpr(gran, "published_at"))
	since := sinceDate(days)

	failureMap := make(map[string]int)

	rows, err := s.db.QueryContext(ctx, failQuery, org, repo, entity, since)
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

	deployMap := make(map[string]int)

	dRows, err := s.db.QueryContext(ctx, deployQuery, org, repo, since)
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
	query := fmt.Sprintf(selectReviewLatencyTpl, GroupExpr(gran, "date"), GroupExpr(gran, "pr.created_at"))
	since := sinceDate(days)

	rows, err := s.db.QueryContext(ctx, query, since, org, repo, entity, since)
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
	query := fmt.Sprintf(queryTpl, GroupExpr(gran, col))
	since := sinceDate(days)

	rows, err := s.db.QueryContext(ctx, query, org, repo, entity, since)
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
	query := fmt.Sprintf(selectPRSizeDistributionTpl, GroupExpr(gran, "e.created_at"))
	since := sinceDate(days)

	rows, err := s.db.QueryContext(ctx, query, org, repo, entity, since)
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
	query := fmt.Sprintf(selectForksAndActivityTpl, GroupExpr(gran, "e.date"))
	since := sinceDate(days)

	rows, err := s.db.QueryContext(ctx, query, org, repo, entity, since)
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
	query := fmt.Sprintf(selectContributorFunnelTpl,
		GroupExpr(gran, "date"),
		GroupExpr(gran, "f.first_comment"),
		GroupExpr(gran, "f.first_pr"),
		GroupExpr(gran, "f.first_merge"),
		GroupExpr(gran, "COALESCE(f.first_comment, f.first_pr, f.first_merge)"),
	)
	since := sinceDate(days)

	rows, err := s.db.QueryContext(ctx, query, org, repo, entity, since)
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
	query := fmt.Sprintf(selectContributorMomentumTpl, GroupExpr(gran, "date"), MomentumInterval(gran), MomentumFormat(gran))
	since := sinceDate(days)

	rows, err := s.db.QueryContext(ctx, query, since, org, repo, entity)
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

	var prs, prsMerged, reviews, issues, comments int
	var prSmall, prMedium, prLarge, prXLarge int
	var avgPrs, avgMerged, avgReviews, avgIssues, avgComments float64
	var avgSmall, avgMedium, avgLarge, avgXLarge float64

	err := s.db.QueryRowContext(ctx, selectContributorProfileSQL,
		username, org, repo, entity, since,
		org, repo, entity, since,
	).Scan(
		&prs, &prsMerged, &reviews, &issues, &comments,
		&prSmall, &prMedium, &prLarge, &prXLarge,
		&avgPrs, &avgMerged, &avgReviews, &avgIssues, &avgComments,
		&avgSmall, &avgMedium, &avgLarge, &avgXLarge,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query contributor profile: %w", err)
	}

	result := &data.ContributorProfileSeries{
		Metrics: []string{"PRs Opened", "PRs Merged", "PR Reviews", "Issues Opened", "Issue Comments",
			"PR Size S", "PR Size M", "PR Size L", "PR Size XL"},
		Values:   []int{prs, prsMerged, reviews, issues, comments, prSmall, prMedium, prLarge, prXLarge},
		Averages: []float64{avgPrs, avgMerged, avgReviews, avgIssues, avgComments, avgSmall, avgMedium, avgLarge, avgXLarge},
	}

	var rep sql.NullFloat64
	if scanErr := s.db.QueryRowContext(ctx, `SELECT reputation FROM developer WHERE username = $1`, username).Scan(&rep); scanErr == nil && rep.Valid {
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
	var total, over30, over90 int

	if err := s.db.QueryRowContext(ctx, selectAgingPRsSQL, org, repo, entity, since).Scan(&total, &over30, &over90); err != nil {
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
	var total, unanswered int

	if err := s.db.QueryRowContext(ctx, selectUnansweredRateSQL, org, repo, entity, since).Scan(&total, &unanswered); err != nil {
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
	var total, withinSLO int

	if err := s.db.QueryRowContext(ctx, selectResponseSLOSQL, org, repo, entity, since).Scan(&total, &withinSLO); err != nil {
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
	thirtyDaysAgo := sinceDate(1)

	var ps data.PortfolioSummary
	if err := s.db.QueryRowContext(ctx, selectPortfolioSummarySQL, org, repo, since, thirtyDaysAgo).Scan(
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

	rows, err := s.db.QueryContext(ctx, selectSignalsSQL, org, weekAgo, twoWeeksAgo, limit)
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
