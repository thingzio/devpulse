package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/go-github/v83/github"
	"github.com/mchmarny/reputer/pkg/score"
	"github.com/thingzio/devpulse/pkg/config"
	"github.com/thingzio/devpulse/pkg/data"
	"github.com/thingzio/devpulse/pkg/data/ghutil"
	"github.com/thingzio/devpulse/pkg/net"
)

const (
	reputationStaleHours = 24

	// selectStaleReputationUsernamesSQL: $1=org, $2=repo, $3=threshold
	selectStaleReputationUsernamesSQL = `SELECT DISTINCT d.username
		FROM devpulse_developer d
		JOIN devpulse_event e ON d.username = e.username
		WHERE 1=1
		  ` + botExcludeDSQL + `
		  ` + forkExcludeSQL + `
		  AND e.org = COALESCE($1, e.org)
		  AND e.repo = COALESCE($2, e.repo)
		  AND (d.reputation_deep IS NULL OR d.reputation_deep = 0)
		  AND (d.reputation IS NULL
		   OR d.reputation_updated_at IS NULL
		   OR d.reputation_updated_at < $3)
	`

	// updateReputationSQL: $1=reputation, $2=updated_at, $3=deep, $4=signals, $5=username
	updateReputationSQL = `UPDATE devpulse_developer
		SET reputation = $1, reputation_updated_at = $2, reputation_deep = $3,
		    reputation_signals = $4
		WHERE username = $5
	`

	// selectUserReputationSQL: $1=username, $2=threshold
	selectUserReputationSQL = `SELECT reputation, reputation_signals
		FROM devpulse_developer
		WHERE username = $1
		  AND reputation IS NOT NULL
		  AND reputation_deep = 1
		  AND reputation_updated_at IS NOT NULL
		  AND reputation_updated_at >= $2
	`

	// selectReputationCompositionSQL: $1=org, $2=repo, $3=entity, $4=since
	selectReputationCompositionSQL = `SELECT
		COUNT(*) FILTER (WHERE d.reputation < 0.3)                         AS alert,
		COUNT(*) FILTER (WHERE d.reputation >= 0.3 AND d.reputation < 0.7) AS standard,
		COUNT(*) FILTER (WHERE d.reputation >= 0.7)                        AS high_confidence,
		COUNT(*) FILTER (WHERE d.reputation_deep = 1)                      AS deep,
		COUNT(*)                                                           AS scored
		FROM (
			SELECT DISTINCT d.username, d.reputation, d.reputation_deep
			FROM devpulse_developer d
			JOIN devpulse_event e ON d.username = e.username
			WHERE e.org = COALESCE($1, e.org)
			  AND e.repo = COALESCE($2, e.repo)
			  AND COALESCE(d.entity, '') = COALESCE($3, COALESCE(d.entity, ''))
			  AND e.date >= $4
			  AND d.reputation IS NOT NULL
			  ` + botExcludeDSQL + `
			  ` + forkExcludeSQL + `
		) d
	`

	// selectReputationTotalSQL: $1=org, $2=repo, $3=entity, $4=since
	selectReputationTotalSQL = `SELECT COUNT(DISTINCT e.username)
		FROM devpulse_event e
		JOIN devpulse_developer d ON e.username = d.username
		WHERE e.org = COALESCE($1, e.org)
		  AND e.repo = COALESCE($2, e.repo)
		  AND COALESCE(d.entity, '') = COALESCE($3, COALESCE(d.entity, ''))
		  AND e.date >= $4
		  ` + botExcludeDSQL + `
		  ` + forkExcludeSQL + `
	`

	selectDistinctOrgsSQL = `SELECT DISTINCT org FROM devpulse_event`

	// selectTieredReputationUsernamesSQL selects stale contributors with
	// different staleness thresholds based on their current score.
	// Never deep-scored (reputation_deep IS NULL or 0) → always eligible.
	// Deep-scored with score < boundary → stale after lowThreshold.
	// Deep-scored with score >= boundary → stale after highThreshold.
	// $1=org, $2=repo, $3=low-score threshold time, $4=high-score threshold time,
	// $5=score boundary, $6=limit
	selectTieredReputationUsernamesSQL = `SELECT d.username
		FROM devpulse_developer d
		JOIN devpulse_event e ON d.username = e.username
		WHERE d.reputation IS NOT NULL
		  ` + botExcludeDSQL + `
		  ` + forkExcludeSQL + `
		  AND e.org = COALESCE($1, e.org)
		  AND e.repo = COALESCE($2, e.repo)
		  AND (
		    (d.reputation_deep IS NULL OR d.reputation_deep = 0)
		    OR d.reputation_updated_at IS NULL
		    OR (d.reputation_deep = 1 AND d.reputation < $5 AND d.reputation_updated_at < $3)
		    OR (d.reputation_deep = 1 AND d.reputation >= $5 AND d.reputation_updated_at < $4)
		  )
		GROUP BY d.username, d.reputation
		ORDER BY d.reputation ASC
		LIMIT $6
	`

	// selectUserCommitCountSQL: $1=username, $2=since
	selectUserCommitCountSQL = `SELECT COUNT(*) FROM devpulse_event
		WHERE username = $1 AND date >= $2
	`

	// selectTotalCommitCountSQL: $1=since
	selectTotalCommitCountSQL = `SELECT COUNT(*) FROM devpulse_event
		WHERE date >= $1
	`

	// selectTotalContributorCountSQL: $1=since
	selectTotalContributorCountSQL = `SELECT COUNT(DISTINCT username) FROM devpulse_event
		WHERE date >= $1
	`

	// selectLastCommitDateSQL: $1=username
	selectLastCommitDateSQL = `SELECT MAX(date) FROM devpulse_event
		WHERE username = $1
	`
)

type globalStats struct {
	totalCommits      int64
	totalContributors int
}

func (s *Store) ImportReputation(ctx context.Context, org, repo *string) (*data.ReputationResult, error) {
	if s.db == nil {
		return nil, data.ErrDBNotInitialized
	}

	threshold := time.Now().UTC().Add(-reputationStaleHours * time.Hour).Format("2006-01-02T15:04:05Z")

	usernames, err := s.getStaleReputationUsernames(ctx, org, repo, threshold)
	if err != nil {
		return nil, fmt.Errorf("error getting stale usernames: %w", err)
	}

	if len(usernames) == 0 {
		slog.Debug("reputation up to date")
		return &data.ReputationResult{}, nil
	}

	slog.Info("scoring reputation", "users", len(usernames))

	since := sinceDate(data.EventAgeDaysDefault)

	stats, err := s.computeGlobalStats(ctx, since)
	if err != nil {
		return nil, fmt.Errorf("error computing global stats: %w", err)
	}

	now := time.Now().UTC().Format("2006-01-02T15:04:05Z")
	res := &data.ReputationResult{}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("error starting reputation tx: %w", err)
	}
	defer rollbackTransaction(tx)

	stmt, err := tx.PrepareContext(ctx, updateReputationSQL)
	if err != nil {
		rollbackTransaction(tx)
		return nil, fmt.Errorf("error preparing reputation update: %w", err)
	}
	defer stmt.Close()

	total := len(usernames)
	logEvery := total / 10
	if logEvery < 1 {
		logEvery = 1
	}

	for i, username := range usernames {
		signals := s.gatherLocalSignals(ctx, username, since, stats)
		rep := score.Compute(signals)

		if _, execErr := stmt.ExecContext(ctx, rep, now, 0, nil, username); execErr != nil {
			rollbackTransaction(tx)
			return nil, fmt.Errorf("error updating reputation for %s: %w", username, execErr)
		}
		res.Updated++

		if (i+1)%logEvery == 0 {
			slog.Info("reputation progress", "scored", i+1, "total", total)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("error committing reputation tx: %w", err)
	}

	slog.Info("reputation done", "updated", res.Updated)

	return res, nil
}

func (s *Store) ImportDeepReputation(ctx context.Context, tokenFn data.TokenFunc, exhaustFn data.ExhaustFunc, limit, staleHours int, org, repo *string) (*data.DeepReputationResult, error) {
	if s.db == nil {
		return nil, data.ErrDBNotInitialized
	}

	if tokenFn == nil || tokenFn() == "" {
		return nil, errors.New("token is required for deep reputation scoring")
	}

	if limit <= 0 {
		return &data.DeepReputationResult{}, nil
	}

	// staleHours is ignored — tiered rescoring uses config-driven thresholds:
	// DEEPREP_LOW_STALE_HOURS (default 168h/7d) for scores < threshold,
	// DEEPREP_HIGH_STALE_HOURS (default 720h/30d) for scores >= threshold.
	_ = staleHours

	// Default to no-op if caller doesn't support exhaustion.
	if exhaustFn == nil {
		exhaustFn = func(string) {}
	}

	now := time.Now().UTC()
	lowStaleHours := config.DeepRepLowScoreStaleHours()
	highStaleHours := config.DeepRepHighScoreStaleHours()
	scoreBoundary := config.DeepRepScoreThreshold()

	lowThreshold := now.Add(-time.Duration(lowStaleHours) * time.Hour).Format("2006-01-02T15:04:05Z")
	highThreshold := now.Add(-time.Duration(highStaleHours) * time.Hour).Format("2006-01-02T15:04:05Z")

	usernames, err := s.getTieredReputationUsernames(ctx, org, repo, lowThreshold, highThreshold, scoreBoundary, limit)
	if err != nil {
		return nil, fmt.Errorf("error getting tiered reputation usernames: %w", err)
	}

	if len(usernames) == 0 {
		slog.Info("deep reputation: no candidates",
			"low_stale_hours", lowStaleHours,
			"high_stale_hours", highStaleHours,
			"score_threshold", scoreBoundary)
		return &data.DeepReputationResult{}, nil
	}

	slog.Info("deep reputation scoring",
		"candidates", len(usernames),
		"low_stale_hours", lowStaleHours,
		"high_stale_hours", highStaleHours,
		"score_threshold", scoreBoundary)

	res := &data.DeepReputationResult{}

	i := 0
	for i < len(usernames) {
		username := usernames[i]
		token := tokenFn()
		if token == "" {
			slog.Warn("all tokens exhausted, stopping deep reputation")
			break
		}

		slog.Info("reputation", "user", username, "index", i+1, "total", len(usernames))

		if _, deepErr := s.ComputeDeepReputation(ctx, token, username); deepErr != nil {
			// Rate limited — exhaust this token and retry same user with next token.
			if ghutil.IsRateLimited(deepErr) {
				exhaustFn(token)
				slog.Warn("token rate limited, exhausting and retrying", "username", username)
				continue // retry same i with next token
			}

			// If user is deleted/renamed (404), mark as deep-scored so they
			// drop out of the candidate pool and stop burning API calls.
			var ghErr *github.ErrorResponse
			if errors.As(deepErr, &ghErr) && ghErr.Response != nil && ghErr.Response.StatusCode == http.StatusNotFound {
				slog.Debug("user not found on github, marking deep-scored", "username", username)
				now := time.Now().UTC().Format("2006-01-02T15:04:05Z")
				if updateErr := s.updateReputation(ctx, username, 0, now, true, nil); updateErr != nil {
					slog.Warn("marking gone user deep-scored", "username", username, "error", updateErr)
				}
			} else {
				slog.Error("deep reputation failed", "username", username, "error", deepErr)
			}
			res.Errors++
			i++
			continue
		}

		res.Scored++
		i++
	}

	skipped := len(usernames) - res.Scored - res.Errors
	slog.Info("deep reputation done",
		"scored", res.Scored,
		"errors", res.Errors,
		"skipped", skipped,
		"candidates", len(usernames))

	return res, nil
}

func (s *Store) GetOrComputeDeepReputation(ctx context.Context, token, username string) (*data.UserReputation, error) {
	if s.db == nil {
		return nil, data.ErrDBNotInitialized
	}

	threshold := time.Now().UTC().Add(-reputationStaleHours * time.Hour).Format("2006-01-02T15:04:05Z")

	var rep float64
	var signalsJSON sql.NullString
	err := s.db.QueryRowContext(ctx, selectUserReputationSQL, username, threshold).Scan(&rep, &signalsJSON)
	if err == nil {
		result := &data.UserReputation{
			Username:   username,
			Reputation: rep,
			Deep:       true,
		}
		if signalsJSON.Valid && signalsJSON.String != "" {
			var ss data.SignalSummary
			if jsonErr := json.Unmarshal([]byte(signalsJSON.String), &ss); jsonErr == nil {
				result.Signals = &ss
			}
		}
		return result, nil
	}

	return s.ComputeDeepReputation(ctx, token, username)
}

func (s *Store) ComputeDeepReputation(ctx context.Context, token, username string) (*data.UserReputation, error) {
	if s.db == nil {
		return nil, data.ErrDBNotInitialized
	}

	if token == "" || username == "" {
		return nil, errors.New("token and username are required")
	}

	since := sinceDate(data.EventAgeDaysDefault)

	stats, err := s.computeGlobalStats(ctx, since)
	if err != nil {
		return nil, fmt.Errorf("error computing global stats: %w", err)
	}

	orgs, err := s.getDistinctOrgs(ctx)
	if err != nil {
		return nil, fmt.Errorf("error getting distinct orgs: %w", err)
	}

	orgSet := make(map[string]bool, len(orgs))
	for _, o := range orgs {
		orgSet[strings.ToLower(o)] = true
	}

	client := github.NewClient(net.GetOAuthClient(ctx, token))

	signals, err := s.gatherFullSignals(ctx, client, username, orgs, orgSet, since, stats)
	if err != nil {
		return nil, fmt.Errorf("error gathering signals for %s: %w", username, err)
	}

	rep := score.Compute(signals)
	now := time.Now().UTC().Format("2006-01-02T15:04:05Z")

	ss := &data.SignalSummary{
		AgeDays:           signals.AgeDays,
		Followers:         signals.Followers,
		Following:         signals.Following,
		PublicRepos:       signals.PublicRepos,
		Suspended:         signals.Suspended,
		OrgMember:         signals.OrgMember,
		Commits:           signals.Commits,
		TotalCommits:      signals.TotalCommits,
		TotalContributors: signals.TotalContributors,
		LastCommitDays:    signals.LastCommitDays,
		AuthorAssociation: signals.AuthorAssociation,
		HasBio:            signals.HasBio,
		HasCompany:        signals.HasCompany,
		HasLocation:       signals.HasLocation,
		HasWebsite:        signals.HasWebsite,
		PRsMerged:         signals.PRsMerged,
		PRsClosed:         signals.PRsClosed,
		RecentPRRepoCount: signals.RecentPRRepoCount,
		ForkedRepos:       signals.ForkedRepos,
		TrustedOrgMember:  signals.TrustedOrgMember,
	}

	if updateErr := s.updateReputation(ctx, username, rep, now, true, ss); updateErr != nil {
		return nil, fmt.Errorf("error storing reputation for %s: %w", username, updateErr)
	}

	return &data.UserReputation{
		Username:   username,
		Reputation: rep,
		Deep:       true,
		Signals:    ss,
	}, nil
}

func (s *Store) GetReputationComposition(ctx context.Context, org, repo, entity *string, days int) (*data.ReputationComposition, error) {
	if s.db == nil {
		return nil, data.ErrDBNotInitialized
	}

	since := sinceDate(days)

	c := &data.ReputationComposition{}

	if err := s.db.QueryRowContext(ctx, selectReputationCompositionSQL, org, repo, entity, since).Scan(
		&c.Alert, &c.Standard, &c.HighConfidence, &c.Deep, &c.Scored,
	); err != nil {
		return nil, fmt.Errorf("failed to query reputation composition: %w", err)
	}

	if err := s.db.QueryRowContext(ctx, selectReputationTotalSQL, org, repo, entity, since).Scan(&c.Total); err != nil {
		return nil, fmt.Errorf("failed to query reputation total: %w", err)
	}

	return c, nil
}

func (s *Store) gatherLocalSignals(ctx context.Context, username, since string, stats *globalStats) score.Signals {
	var sig score.Signals

	var commits int64
	if err := s.db.QueryRowContext(ctx, selectUserCommitCountSQL, username, since).Scan(&commits); err != nil && !errors.Is(err, sql.ErrNoRows) {
		slog.Debug("error counting user commits", "username", username, "error", err)
	}
	sig.Commits = commits

	sig.TotalCommits = stats.totalCommits
	sig.TotalContributors = stats.totalContributors

	var lastDate sql.NullString
	if err := s.db.QueryRowContext(ctx, selectLastCommitDateSQL, username).Scan(&lastDate); err != nil && !errors.Is(err, sql.ErrNoRows) {
		slog.Debug("error getting last commit date", "username", username, "error", err)
	}
	if lastDate.Valid && lastDate.String != "" {
		if t, parseErr := time.Parse("2006-01-02", lastDate.String); parseErr == nil {
			sig.LastCommitDays = int64(time.Since(t).Hours() / 24)
		}
	}

	return sig
}

//nolint:gocyclo // complexity from rate-limit error handling
func (s *Store) gatherFullSignals(ctx context.Context, client *github.Client, username string, orgs []string, orgSet map[string]bool, since string, stats *globalStats) (score.Signals, error) {
	sig := s.gatherLocalSignals(ctx, username, since, stats)

	usr, resp, err := client.Users.Get(ctx, username)
	if err != nil {
		return sig, fmt.Errorf("error getting user %s: %w", username, err)
	}
	if err := ghutil.CheckRateLimit(ctx, resp); err != nil {
		return sig, err
	}

	if usr.CreatedAt != nil {
		sig.AgeDays = int64(time.Since(usr.CreatedAt.Time).Hours() / 24)
	}
	sig.Followers = int64(usr.GetFollowers())
	sig.Following = int64(usr.GetFollowing())
	sig.PublicRepos = int64(usr.GetPublicRepos())
	sig.Suspended = usr.SuspendedAt != nil
	sig.HasBio = usr.GetBio() != ""
	sig.HasCompany = usr.GetCompany() != ""
	sig.HasLocation = usr.GetLocation() != ""
	sig.HasWebsite = usr.GetBlog() != ""

	company := strings.ToLower(strings.TrimSpace(strings.ReplaceAll(usr.GetCompany(), "@", "")))
	if company != "" && orgSet[company] {
		sig.OrgMember = true
	} else {
		for _, org := range orgs {
			isMember, memberResp, memberErr := client.Organizations.IsMember(ctx, org, username)
			if memberErr != nil {
				slog.Debug("error checking org membership", "org", org, "username", username, "error", memberErr)
				continue
			}
			if err := ghutil.CheckRateLimit(ctx, memberResp); err != nil {
				return sig, err
			}
			if isMember {
				sig.OrgMember = true
				break
			}
		}
	}

	sig.TrustedOrgMember = sig.OrgMember

	var forkedCount int64
	const maxRepoPages = 50
	repoOpts := &github.RepositoryListByUserOptions{
		Type:        "owner",
		ListOptions: github.ListOptions{PerPage: 100},
	}
	for page := 0; page < maxRepoPages; page++ {
		repos, repoResp, repoErr := client.Repositories.ListByUser(ctx, username, repoOpts)
		if repoErr != nil {
			slog.Debug("error listing repos for fork count", "username", username, "error", repoErr)
			break
		}
		if err := ghutil.CheckRateLimit(ctx, repoResp); err != nil {
			return sig, err
		}
		for _, r := range repos {
			if r.GetFork() {
				forkedCount++
			}
		}
		if repoResp.NextPage == 0 {
			break
		}
		repoOpts.Page = repoResp.NextPage
	}
	sig.ForkedRepos = forkedCount

	mergedQuery := fmt.Sprintf("type:pr author:%s is:merged", username)
	mergedResult, mergedResp, mergedErr := client.Search.Issues(ctx, mergedQuery, &github.SearchOptions{
		ListOptions: github.ListOptions{PerPage: 1},
	})
	if mergedErr != nil {
		slog.Debug("error searching merged PRs", "username", username, "error", mergedErr)
	} else {
		if err := ghutil.CheckRateLimit(ctx, mergedResp); err != nil {
			return sig, err
		}
		sig.PRsMerged = int64(mergedResult.GetTotal())
	}

	closedQuery := fmt.Sprintf("type:pr author:%s is:unmerged is:closed", username)
	closedResult, closedResp, closedErr := client.Search.Issues(ctx, closedQuery, &github.SearchOptions{
		ListOptions: github.ListOptions{PerPage: 1},
	})
	if closedErr != nil {
		slog.Debug("error searching closed PRs", "username", username, "error", closedErr)
	} else {
		if err := ghutil.CheckRateLimit(ctx, closedResp); err != nil {
			return sig, err
		}
		sig.PRsClosed = int64(closedResult.GetTotal())
	}

	cutoff := time.Now().AddDate(0, 0, -90).Format("2006-01-02")
	recentQuery := fmt.Sprintf("type:pr author:%s created:>=%s", username, cutoff)
	recentRepoSet := make(map[string]bool)
	recentOpts := &github.SearchOptions{
		ListOptions: github.ListOptions{PerPage: 100},
	}
	for {
		recentResult, recentResp, recentErr := client.Search.Issues(ctx, recentQuery, recentOpts)
		if recentErr != nil {
			slog.Debug("error searching recent PRs", "username", username, "error", recentErr)
			break
		}
		if err := ghutil.CheckRateLimit(ctx, recentResp); err != nil {
			return sig, err
		}
		for _, issue := range recentResult.Issues {
			if repoURL := issue.GetRepositoryURL(); repoURL != "" {
				recentRepoSet[repoURL] = true
			}
		}
		if recentResp.NextPage == 0 {
			break
		}
		recentOpts.Page = recentResp.NextPage
	}
	sig.RecentPRRepoCount = int64(len(recentRepoSet))

	return sig, nil
}

func (s *Store) computeGlobalStats(ctx context.Context, since string) (*globalStats, error) {
	var gs globalStats

	if err := s.db.QueryRowContext(ctx, selectTotalCommitCountSQL, since).Scan(&gs.totalCommits); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("error counting total commits: %w", err)
	}

	if err := s.db.QueryRowContext(ctx, selectTotalContributorCountSQL, since).Scan(&gs.totalContributors); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("error counting total contributors: %w", err)
	}

	return &gs, nil
}

func (s *Store) getStaleReputationUsernames(ctx context.Context, org, repo *string, threshold string) ([]string, error) {
	if s.db == nil {
		return nil, data.ErrDBNotInitialized
	}

	rows, err := s.db.QueryContext(ctx, selectStaleReputationUsernamesSQL, org, repo, threshold)
	if err != nil {
		return nil, fmt.Errorf("failed to query stale reputation usernames: %w", err)
	}
	defer rows.Close()

	list := make([]string, 0)
	for rows.Next() {
		var username string
		if err := rows.Scan(&username); err != nil {
			return nil, fmt.Errorf("failed to scan username: %w", err)
		}
		list = append(list, username)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating rows: %w", err)
	}

	return list, nil
}

func (s *Store) getTieredReputationUsernames(ctx context.Context, org, repo *string, lowThreshold, highThreshold string, scoreBoundary float64, limit int) ([]string, error) {
	if s.db == nil {
		return nil, data.ErrDBNotInitialized
	}

	rows, err := s.db.QueryContext(ctx, selectTieredReputationUsernamesSQL, org, repo, lowThreshold, highThreshold, scoreBoundary, limit)
	if err != nil {
		return nil, fmt.Errorf("querying tiered reputation usernames: %w", err)
	}
	defer rows.Close()

	list := make([]string, 0, limit)
	for rows.Next() {
		var username string
		if err := rows.Scan(&username); err != nil {
			return nil, fmt.Errorf("scanning username: %w", err)
		}
		list = append(list, username)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating rows: %w", err)
	}
	return list, nil
}

func (s *Store) updateReputation(ctx context.Context, username string, reputation float64, updatedAt string, deep bool, signals *data.SignalSummary) error {
	if s.db == nil {
		return data.ErrDBNotInitialized
	}

	deepVal := 0
	if deep {
		deepVal = 1
	}

	var signalsJSON *string
	if signals != nil {
		b, err := json.Marshal(signals)
		if err != nil {
			return fmt.Errorf("failed to marshal signals for %s: %w", username, err)
		}
		str := string(b)
		signalsJSON = &str
	}

	// Single UPDATE — no transaction needed. Last write wins, which is correct
	// since reputation scores are deterministic for the same input data.
	if _, err := s.db.ExecContext(ctx, updateReputationSQL, reputation, updatedAt, deepVal, signalsJSON, username); err != nil {
		return fmt.Errorf("failed to update reputation for %s: %w", username, err)
	}

	return nil
}

func (s *Store) getDistinctOrgs(ctx context.Context) ([]string, error) {
	if s.db == nil {
		return nil, data.ErrDBNotInitialized
	}

	rows, err := s.db.QueryContext(ctx, selectDistinctOrgsSQL)
	if err != nil {
		return nil, fmt.Errorf("failed to query distinct orgs: %w", err)
	}
	defer rows.Close()

	list := make([]string, 0)
	for rows.Next() {
		var org string
		if err := rows.Scan(&org); err != nil {
			return nil, fmt.Errorf("failed to scan org: %w", err)
		}
		list = append(list, org)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating rows: %w", err)
	}

	return list, nil
}
