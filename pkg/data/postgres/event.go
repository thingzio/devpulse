package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"log/slog"

	"github.com/google/go-github/v83/github"
	"github.com/thingzio/devpulse/pkg/config"
	"github.com/thingzio/devpulse/pkg/data"
	"github.com/thingzio/devpulse/pkg/data/ghutil"
	"github.com/thingzio/devpulse/pkg/net"
	"golang.org/x/sync/errgroup"
)

func compareEventsByPK(a, b *data.Event) int {
	if c := strings.Compare(a.Org, b.Org); c != 0 {
		return c
	}
	if c := strings.Compare(a.Repo, b.Repo); c != 0 {
		return c
	}
	if c := strings.Compare(a.Username, b.Username); c != 0 {
		return c
	}
	if c := strings.Compare(a.Type, b.Type); c != 0 {
		return c
	}
	return strings.Compare(a.Date, b.Date)
}

const (
	pageSizeDefault = 100
	importBatchSize = 500
	nilNumber       = 0

	sortField        string = "created"
	sortCommentField string = "updated"
	sortForkField    string = "newest"
	sortDirection    string = "desc"
	// selectPRsMissingSizeSQL: $1=org, $2=repo, $3=min_created_at
	selectPRsMissingSizeSQL = `SELECT org, repo, number
		FROM devpulse_event
		WHERE type = 'pr'
		  AND org = $1
		  AND repo = $2
		  AND number IS NOT NULL
		  AND number > 0
		  AND (additions IS NULL OR changed_files IS NULL)
		  AND created_at >= $3
		ORDER BY created_at DESC
		LIMIT 500
	`

	// updatePRSizeSQL: $1=additions, $2=deletions, $3=changed_files, $4=commits,
	//                  $5=org, $6=repo, $7=number
	updatePRSizeSQL = `UPDATE devpulse_event
		SET additions = $1, deletions = $2, changed_files = $3, commits = $4
		WHERE type = 'pr' AND org = $5 AND repo = $6 AND number = $7
	`

	// insertEventSQL: 18 INSERT params + 13 ON CONFLICT params = 31 total
	insertEventSQL = `INSERT INTO devpulse_event (
			org, repo, username, type, date, url, mentions, labels,
			state, number, created_at, closed_at, merged_at, additions, deletions,
			changed_files, commits, title
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18)
		ON CONFLICT(org, repo, username, type, date) DO UPDATE SET
			url = $19, mentions = $20, labels = $21,
			state = COALESCE($22, devpulse_event.state),
			number = COALESCE($23, devpulse_event.number),
			created_at = COALESCE($24, devpulse_event.created_at),
			closed_at = COALESCE($25, devpulse_event.closed_at),
			merged_at = COALESCE($26, devpulse_event.merged_at),
			additions = COALESCE($27, devpulse_event.additions),
			deletions = COALESCE($28, devpulse_event.deletions),
			changed_files = COALESCE($29, devpulse_event.changed_files),
			commits = COALESCE($30, devpulse_event.commits),
			title = $31
	`
)

// dbBatchSize is the number of events per DB transaction during flush.
// Initialized from IMPORT_DB_BATCH_SIZE env var (default 100).
var dbBatchSize = config.ImportDBBatchSize()

var EventTypes = []string{
	data.EventTypePR,
	data.EventTypeIssue,
	data.EventTypeIssueComment,
	data.EventTypePRReview,
	data.EventTypeFork,
}

type importerFunc func(ctx context.Context) error

func (s *Store) UpdateEvents(ctx context.Context, token string, concurrency int) (map[string]int, error) {
	if s.db == nil {
		return nil, data.ErrDBNotInitialized
	}

	if token == "" {
		return nil, errors.New("token is required")
	}

	if concurrency < 1 {
		concurrency = 1
	}

	list, err := s.GetAllOrgRepos(ctx)
	if err != nil {
		return nil, fmt.Errorf("error getting org/repo list: %w", err)
	}

	results := make(map[string]int)
	var mu sync.Mutex
	var failed int

	// errgroup.WithContext propagates parent cancellation but per-repo errors
	// are logged and swallowed — one bad repo must not abort the whole batch.
	// We track failure count under the mutex and surface it as a summary
	// error after Wait so callers can detect partial failures.
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(concurrency)

	for _, r := range list {
		org, repo := r.Org, r.Repo
		g.Go(func() error {
			// Honor parent cancellation; returning ctx.Err() short-circuits
			// the remaining queued goroutines without faulting them as failures.
			if err := gctx.Err(); err != nil {
				return err
			}
			staticToken := func() string { return token }
			windowStart := time.Now().AddDate(0, 0, -data.EventAgeDaysDefault).UTC()
			windowEnd := time.Now().UTC()
			m, _, importErr := s.ImportEvents(gctx, staticToken, nil, org, repo, windowStart, windowEnd)
			if importErr != nil {
				slog.Error("error importing events", "org", org, "repo", repo, "error", importErr)
				mu.Lock()
				failed++
				mu.Unlock()
				return nil // log and continue, don't abort other repos
			}

			mu.Lock()
			for k, v := range m {
				results[k] += v
			}
			mu.Unlock()

			return nil
		})
	}

	_ = g.Wait()
	if failed > 0 {
		return results, fmt.Errorf("event update completed with %d failed repos", failed)
	}
	return results, nil
}

func (s *Store) ImportEvents(
	ctx context.Context, tokenFn data.TokenFunc, exhaustFn data.ExhaustFunc,
	owner, repo string, windowStart, windowEnd time.Time,
) (map[string]int, *data.ImportSummary, error) {
	if s.db == nil {
		return nil, nil, data.ErrDBNotInitialized
	}

	if tokenFn == nil || owner == "" || repo == "" {
		return nil, nil, errors.New("tokenFn, owner, and repo are required")
	}

	if windowStart.IsZero() {
		windowStart = time.Now().AddDate(0, 0, -data.EventAgeDaysDefault).UTC()
	}
	// windowEnd reserved for future use (e.g. bounded backfill windows).
	_ = windowEnd

	token := tokenFn()
	if token == "" {
		return nil, nil, errors.New("token pool exhausted")
	}
	client := github.NewClient(net.GetOAuthClient(ctx, token))

	imp := &eventImporter{
		tokenFn:      tokenFn,
		exhaustFn:    exhaustFn,
		curToken:     token,
		client:       client,
		store:        s,
		owner:        owner,
		repo:         repo,
		list:         make([]*data.Event, 0),
		counts:       make(map[string]int),
		users:        make(map[string]*github.User),
		state:        make(map[string]*data.State),
		minEventTime: windowStart,
	}

	importers := []importerFunc{
		imp.importPREvents,
		imp.importPRReviewEvents,
		imp.importIssueEvents,
		imp.importIssueCommentEvents,
		imp.importForkEvents,
	}

	if err := imp.loadState(ctx); err != nil {
		return nil, nil, fmt.Errorf("error loading last page state: %s/%s: %w", owner, repo, err)
	}

	var earliest time.Time
	for _, t := range EventTypes {
		st := imp.state[t]
		if earliest.IsZero() || st.Since.Before(earliest) {
			earliest = st.Since
		}
		slog.Debug("resume state",
			"org", owner,
			"repo", repo,
			"type", t,
			"since", st.Since.Format("2006-01-02"),
			"page", st.Page)
	}
	slog.Info("importing events",
		"org", owner,
		"repo", repo,
		"since", earliest.Format("2006-01-02"))
	// errgroup.WithContext propagates ctx cancellation to all importer
	// goroutines if the parent ctx is canceled (worker shutdown). Per-phase
	// errors are intentionally swallowed: one phase failing should not
	// cancel siblings, since each tracks independent state. We capture the
	// first error so the caller knows imports were partial.
	g, gctx := errgroup.WithContext(ctx)
	var firstErr error
	var firstErrOnce sync.Once
	for i := range importers {
		fn := importers[i]
		g.Go(func() error {
			if err := fn(gctx); err != nil {
				slog.Error("event import failed", "org", owner, "repo", repo, "error", err)
				firstErrOnce.Do(func() { firstErr = err })
			}
			return nil // log and continue, don't cancel siblings
		})
	}

	_ = g.Wait()
	if firstErr != nil && ctx.Err() != nil {
		// Parent context canceled mid-flight — propagate so the caller
		// can short-circuit instead of attempting a final flush.
		return nil, nil, fmt.Errorf("event import canceled: %w", ctx.Err())
	}

	if err := imp.flush(ctx); err != nil {
		return nil, nil, fmt.Errorf("error flushing final events: %s/%s: %w", imp.owner, imp.repo, err)
	}

	if err := imp.backfillPRSize(ctx); err != nil {
		if ghutil.IsRateLimited(err) {
			slog.Warn("backfill rate limited", "org", owner, "repo", repo, "error", err)
		} else {
			slog.Warn("error backfilling PR size data", "org", owner, "repo", repo, "error", err)
		}
	}

	total := 0
	for _, v := range imp.counts {
		total += v
	}
	slog.Info("events imported",
		"org", owner,
		"repo", repo,
		"events", total,
		"developers", len(imp.users),
		"since", earliest.Format("2006-01-02"))

	summary := &data.ImportSummary{
		Repo:       owner + "/" + repo,
		Since:      windowStart.Format("2006-01-02"),
		Events:     total,
		Developers: len(imp.users),
	}

	return imp.counts, summary, nil
}

type eventImporter struct {
	mu           sync.Mutex
	tokenFn      data.TokenFunc
	exhaustFn    data.ExhaustFunc
	curToken     string
	client       *github.Client
	store        *Store
	owner        string
	repo         string
	list         []*data.Event
	counts       map[string]int
	users        map[string]*github.User
	state        map[string]*data.State
	minEventTime time.Time
	flushed      int
}

// rotateToken marks the current token as exhausted and gets a fresh one.
// Returns true if a new token was obtained.
func (e *eventImporter) rotateToken(ctx context.Context) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.exhaustFn != nil && e.curToken != "" {
		e.exhaustFn(e.curToken)
	}
	e.curToken = e.tokenFn()
	if e.curToken == "" {
		return false
	}
	e.client = github.NewClient(net.GetOAuthClient(ctx, e.curToken))
	return true
}

// retryOnRateLimit executes fn. If it returns a rate-limit error, rotates
// the token and retries once. If it returns a GitHub server error (5xx),
// backs off briefly and retries once. Returns the original error if
// rotation fails or the retry also fails.
func (e *eventImporter) retryOnRateLimit(ctx context.Context, fn func() error) error {
	err := fn()
	if err == nil {
		return nil
	}
	if ghutil.IsRateLimited(err) {
		slog.Debug("rate limited, rotating token", "org", e.owner, "repo", e.repo)
		if !e.rotateToken(ctx) {
			return fmt.Errorf("all tokens exhausted: %w", err)
		}
		return fn()
	}
	if ghutil.IsServerError(err) {
		slog.Warn("github server error, retrying after backoff",
			"org", e.owner, "repo", e.repo, "error", err)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
		if retryErr := fn(); retryErr != nil {
			return fmt.Errorf("server error after retry: %w", retryErr)
		}
		return nil
	}
	return fmt.Errorf("non-retryable error: %w", err)
}

func (e *eventImporter) qualifyTypeKey(t string) string {
	return e.owner + "/" + e.repo + "/" + t
}

func (e *eventImporter) updatePage(eventType string, page int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if s, ok := e.state[eventType]; ok {
		s.Page = page
	}
}

type eventExtra struct {
	State        *string
	Number       *int
	CreatedAt    *string
	ClosedAt     *string
	MergedAt     *string
	Additions    *int
	Deletions    *int
	ChangedFiles *int
	Commits      *int
	Title        string
}

func (e *eventImporter) add(ctx context.Context, eType, url string, usr *github.User, updated *time.Time, mentions []string, labels []string, extra *eventExtra) error {
	if usr == nil || usr.GetLogin() == "" {
		return nil
	}

	item := &data.Event{
		Org:      e.owner,
		Repo:     e.repo,
		Username: usr.GetLogin(),
		Type:     eType,
		Date:     ghutil.ParseDate(updated),
		URL:      url,
		Mentions: strings.Join(unique(mentions), ","),
		Labels:   strings.Join(unique(labels), ","),
	}

	if extra != nil {
		item.State = extra.State
		item.Number = extra.Number
		item.CreatedAt = extra.CreatedAt
		item.ClosedAt = extra.ClosedAt
		item.MergedAt = extra.MergedAt
		item.Additions = extra.Additions
		item.Deletions = extra.Deletions
		item.ChangedFiles = extra.ChangedFiles
		item.Commits = extra.Commits
		item.Title = extra.Title
	}

	e.mu.Lock()
	e.list = append(e.list, item)
	e.counts[e.qualifyTypeKey(eType)]++
	e.users[item.Username] = usr
	shouldFlush := len(e.list) >= importBatchSize
	e.mu.Unlock()

	if shouldFlush {
		if err := e.flush(ctx); err != nil {
			return fmt.Errorf("error flushing events: %w", err)
		}
	}
	return nil
}

func (e *eventImporter) loadState(ctx context.Context) error {
	for _, t := range EventTypes {
		state, err := e.store.GetState(ctx, t, e.owner, e.repo, e.minEventTime)
		if err != nil {
			return fmt.Errorf("error getting last page: %s/%s - %s: %w", e.owner, e.repo, t, err)
		}
		// Always start from page 1. Results are sorted newest-first (created desc),
		// so resuming from a saved page skips new events created since the last run.
		state.Page = 1

		// Clamp since to minEventTime — stored timestamps can be arbitrarily old
		// and cause GitHub API 500s on large repos (e.g. kubernetes/kubernetes).
		if state.Since.Before(e.minEventTime) {
			state.Since = e.minEventTime
		}

		e.state[t] = state
	}

	return nil
}

func splitIntoBatches[T any](items []T, size int) [][]T {
	if len(items) == 0 || size < 1 {
		return nil
	}

	batches := make([][]T, 0, (len(items)+size-1)/size)
	for i := 0; i < len(items); i += size {
		end := i + size
		if end > len(items) {
			end = len(items)
		}
		batches = append(batches, items[i:end])
	}

	return batches
}

func (e *eventImporter) flush(ctx context.Context) error {
	start := time.Now()

	var events []*data.Event
	var users map[string]*github.User
	var state map[string]*data.State

	e.mu.Lock()
	if len(e.list) == 0 {
		e.mu.Unlock()
		return nil
	}
	events = e.list
	e.list = make([]*data.Event, 0)

	users = make(map[string]*github.User, len(e.users))
	for k, v := range e.users {
		users[k] = v
	}

	state = make(map[string]*data.State, len(e.state))
	for k, v := range e.state {
		cp := *v
		state[k] = &cp
	}
	e.mu.Unlock()

	slices.SortFunc(events, compareEventsByPK)

	slog.Debug("flushing events and developers to db",
		"events", len(events), "developers", len(users), "db_batch_size", dbBatchSize)

	db := e.store.db

	eventStmt, err := db.PrepareContext(ctx, insertEventSQL)
	if err != nil {
		return fmt.Errorf("failed to prepare event insert statement: %w", err)
	}
	defer eventStmt.Close()

	devStmt, err := db.PrepareContext(ctx, insertDeveloperSQL)
	if err != nil {
		return fmt.Errorf("failed to prepare developer insert statement: %w", err)
	}
	defer devStmt.Close()

	batches := splitIntoBatches(events, dbBatchSize)

	for _, batch := range batches {
		if batchErr := e.flushBatch(ctx, db, batch, users, devStmt, eventStmt); batchErr != nil {
			return batchErr
		}
	}

	// Save state once after all sub-batches complete.
	stateStmt, err := db.PrepareContext(ctx, insertStateSQL)
	if err != nil {
		return fmt.Errorf("failed to prepare state insert statement: %w", err)
	}
	defer stateStmt.Close()

	for t, p := range state {
		since := p.Since.Unix()
		var backfillUnix sql.NullInt64
		if p.BackfillUntil != nil {
			backfillUnix = sql.NullInt64{Int64: p.BackfillUntil.Unix(), Valid: true}
		}
		_, err = stateStmt.ExecContext(ctx, t, e.owner, e.repo, p.Page, since, backfillUnix,
			p.Page, since, backfillUnix)
		if err != nil {
			return fmt.Errorf("error inserting state[%s]: %s/%s with page:%d and since:%s: %w",
				t, e.owner, e.repo, p.Page, p.Since.Format("2006-01-02"), err)
		}
	}

	e.mu.Lock()
	e.flushed += len(events)
	total := e.flushed
	e.mu.Unlock()

	slog.Info("events progress",
		"org", e.owner,
		"repo", e.repo,
		"batch", len(events),
		"total", total,
		"developers", len(users),
		"duration_sec", time.Since(start).Seconds())

	return nil
}

// flushBatch inserts a single batch of events and developers inside a
// transaction. Extracted from the flush loop so defer operates per-batch.
func (e *eventImporter) flushBatch(ctx context.Context, db DBTX,
	batch []*data.Event, users map[string]*github.User,
	devStmt, eventStmt *sql.Stmt) error {
	seen := make(map[string]struct{}, len(batch))
	for _, ev := range batch {
		seen[ev.Username] = struct{}{}
	}
	batchDevs := make([]*data.Developer, 0, len(seen))
	for username := range seen {
		if u, ok := users[username]; ok {
			batchDevs = append(batchDevs, ghutil.MapUserToDeveloper(u))
		}
	}
	slices.SortFunc(batchDevs, func(a, b *data.Developer) int {
		return strings.Compare(a.Username, b.Username)
	})

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer rollbackTransaction(tx)

	txDevStmt := tx.Stmt(devStmt)
	defer txDevStmt.Close()
	for i, u := range batchDevs {
		if _, err = txDevStmt.ExecContext(ctx, u.Username,
			u.FullName, u.Email, u.AvatarURL, u.ProfileURL, u.Entity,
			u.FullName, u.Email, u.AvatarURL, u.ProfileURL, u.Entity, u.Entity); err != nil {
			return fmt.Errorf("error inserting developer[%d]: %s: %w", i, u.Username, err)
		}
	}

	txEventStmt := tx.Stmt(eventStmt)
	defer txEventStmt.Close()
	for i, ev := range batch {
		_, err = txEventStmt.ExecContext(ctx,
			ev.Org, ev.Repo, ev.Username, ev.Type, ev.Date,
			ev.URL, ev.Mentions, ev.Labels,
			ev.State, ev.Number, ev.CreatedAt, ev.ClosedAt, ev.MergedAt, ev.Additions, ev.Deletions,
			ev.ChangedFiles, ev.Commits, ev.Title,
			ev.URL, ev.Mentions, ev.Labels,
			ev.State, ev.Number, ev.CreatedAt, ev.ClosedAt, ev.MergedAt, ev.Additions, ev.Deletions,
			ev.ChangedFiles, ev.Commits, ev.Title,
		)
		if err != nil {
			return fmt.Errorf("error inserting event[%d]: %s/%s: %w", i, ev.Org, ev.Repo, err)
		}
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}
	return nil
}

func timestampToTime(ts *github.Timestamp) *time.Time {
	if ts == nil {
		return nil
	}
	return &ts.Time
}

func (e *eventImporter) isEventBatchValidAge(first *time.Time, last *time.Time) bool {
	if first == nil || last == nil {
		return false
	}

	if first.Before(e.minEventTime) && last.Before(e.minEventTime) {
		return false
	}

	return true
}

func timestampStr(ts *github.Timestamp) *string {
	if ts == nil {
		return nil
	}
	s := ts.Format("2006-01-02T15:04:05Z")
	return &s
}

func intPtr(v int) *int {
	if v == 0 {
		return nil
	}
	return &v
}

func parseIssueNumberFromURL(url string) int {
	parts := strings.Split(url, "/")
	for i, p := range parts {
		if p == "issues" && i+1 < len(parts) {
			numStr := strings.SplitN(parts[i+1], "#", 2)[0]
			n, err := strconv.Atoi(numStr)
			if err != nil {
				return 0
			}
			return n
		}
	}
	return 0
}

func parsePRNumberFromURL(url string) int {
	parts := strings.Split(url, "/")
	if len(parts) == 0 {
		return 0
	}
	n, err := strconv.Atoi(parts[len(parts)-1])
	if err != nil {
		return 0
	}
	return n
}

//nolint:dupl // pagination boilerplate shared with importIssueEvents/importForkEvents; different API + item processing
func (e *eventImporter) importPREvents(ctx context.Context) error {
	slog.Debug("starting pr event import", "page", e.state[data.EventTypePR].Page, "since", e.state[data.EventTypePR].Since.Format("2006-01-02"))

	opt := &github.PullRequestListOptions{
		State:     "all",
		Sort:      sortField,
		Direction: sortDirection,
		ListOptions: github.ListOptions{
			PerPage: pageSizeDefault,
			Page:    e.state[data.EventTypePR].Page,
		},
	}

	for {
		var items []*github.PullRequest
		var resp *github.Response
		if err := e.retryOnRateLimit(ctx, func() error {
			var apiErr error
			items, resp, apiErr = e.client.PullRequests.List(ctx, e.owner, e.repo, opt)
			if apiErr != nil {
				return fmt.Errorf("error listing prs: %w", apiErr)
			}
			if resp.StatusCode != http.StatusOK {
				net.PrintHTTPResponse(resp.Response)
				return fmt.Errorf("error listing prs, rate: %s, status: %d", ghutil.RateInfo(&resp.Rate), resp.StatusCode)
			}
			return ghutil.CheckRateLimit(ctx, resp)
		}); err != nil {
			return fmt.Errorf("listing PRs: %w", err)
		}
		slog.Debug("pr events", "found", len(items), "next_page", resp.NextPage, "last_page", resp.LastPage, "rate", ghutil.RateInfo(&resp.Rate))

		if len(items) == 0 {
			break
		}

		if !e.isEventBatchValidAge(timestampToTime(items[0].CreatedAt), timestampToTime(items[len(items)-1].CreatedAt)) {
			slog.Debug("pr - all returned events older than min", "min_event_time", e.minEventTime.Format("2006-01-02"))
			break
		}

		for i := range items {
			mentions := ghutil.ParseUsers(items[i].Body)
			mentions = append(mentions, ghutil.GetUsernames(items[i].Assignee)...)
			mentions = append(mentions, ghutil.GetUsernames(items[i].Assignees...)...)
			mentions = append(mentions, ghutil.GetUsernames(items[i].RequestedReviewers...)...)
			extra := &eventExtra{
				State:     items[i].State,
				Number:    items[i].Number,
				CreatedAt: timestampStr(items[i].CreatedAt),
				ClosedAt:  timestampStr(items[i].ClosedAt),
				MergedAt:  timestampStr(items[i].MergedAt),
				Additions: intPtr(items[i].GetAdditions()),
				Deletions: intPtr(items[i].GetDeletions()),
				Title:     items[i].GetTitle(),
			}
			// Use CreatedAt — not UpdatedAt — so the (org, repo, user, type, date)
			// PK stays stable across re-imports. UpdatedAt shifts on every comment,
			// review, or merge, producing duplicate rows that never converge to the
			// latest state.
			if err := e.add(ctx, data.EventTypePR, *items[i].HTMLURL, items[i].User, timestampToTime(items[i].CreatedAt), mentions,
				ghutil.GetLabels(items[i].Labels), extra); err != nil {
				return fmt.Errorf("error adding pr event: %s/%s: %w", e.owner, e.repo, err)
			}

			if err := e.importPRReviews(ctx, items[i].GetNumber()); err != nil {
				slog.Warn("error importing PR reviews", "pr", items[i].GetNumber(), "error", err)
			}
		}

		e.updatePage(data.EventTypePR, opt.ListOptions.Page)

		if resp.NextPage == 0 {
			break
		}

		opt.ListOptions.Page = resp.NextPage
	}

	return nil
}

type prRef struct {
	org, repo string
	number    int
}

func (e *eventImporter) backfillPRSize(ctx context.Context) error {
	db := e.store.db

	days := config.BackfillMaxDays()
	minCreatedAt := time.Now().AddDate(0, 0, -days).UTC().Format(time.RFC3339)
	rows, err := db.QueryContext(ctx, selectPRsMissingSizeSQL, e.owner, e.repo, minCreatedAt)
	if err != nil {
		return fmt.Errorf("error querying PRs missing size: %w", err)
	}
	defer rows.Close()

	var prs []prRef
	for rows.Next() {
		var r prRef
		if err := rows.Scan(&r.org, &r.repo, &r.number); err != nil {
			return fmt.Errorf("error scanning PR ref: %w", err)
		}
		prs = append(prs, r)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("error iterating PR refs: %w", err)
	}

	if len(prs) == 0 {
		return nil
	}

	slog.Info("backfilling PR sizes", "org", e.owner, "repo", e.repo, "total", len(prs))
	updated := 0
	for i, p := range prs {
		if i > 0 && i%50 == 0 {
			slog.Info("PR backfill progress", "org", e.owner, "repo", e.repo, "processed", i, "total", len(prs))
		}
		if ctx.Err() != nil {
			return fmt.Errorf("backfill PR size canceled: %w", ctx.Err())
		}
		ok, err := e.fetchAndUpdatePRSize(ctx, db, p)
		if err != nil {
			return fmt.Errorf("backfill PR size for %s/%s#%d: %w", p.org, p.repo, p.number, err)
		}
		if ok {
			updated++
		}
	}

	slog.Info("PR backfill complete", "org", e.owner, "repo", e.repo, "updated", updated, "total", len(prs))
	return nil
}

func (e *eventImporter) fetchAndUpdatePRSize(ctx context.Context, db DBTX, p prRef) (bool, error) {
	var pr *github.PullRequest
	var resp *github.Response
	if err := e.retryOnRateLimit(ctx, func() error {
		var apiErr error
		pr, resp, apiErr = e.client.PullRequests.Get(ctx, e.owner, e.repo, p.number)
		if apiErr != nil {
			return apiErr
		}
		return ghutil.CheckRateLimit(ctx, resp)
	}); err != nil {
		if ghutil.IsRateLimited(err) {
			return false, fmt.Errorf("rate limit hit during PR backfill: %w", err)
		}
		if wait := ghutil.AbuseRetryAfter(err); wait > 0 {
			slog.Warn("secondary rate limit hit, waiting", "number", p.number, "wait", wait.String())
			select {
			case <-ctx.Done():
				return false, ctx.Err()
			case <-time.After(wait):
			}
			pr, resp, err = e.client.PullRequests.Get(ctx, e.owner, e.repo, p.number)
			if err != nil {
				slog.Warn("error fetching PR details after retry", "number", p.number, "error", err)
				return e.markPRSizeZero(ctx, db, p)
			}
		} else {
			slog.Warn("error fetching PR details", "number", p.number, "error", err)
			return e.markPRSizeZero(ctx, db, p)
		}
	}
	if resp.StatusCode != http.StatusOK {
		return e.markPRSizeZero(ctx, db, p)
	}

	additions := pr.GetAdditions()
	deletions := pr.GetDeletions()
	changedFiles := pr.GetChangedFiles()
	commits := pr.GetCommits()

	if _, err := db.ExecContext(ctx, updatePRSizeSQL,
		intPtr(additions), intPtr(deletions), intPtr(changedFiles), intPtr(commits),
		p.org, p.repo, p.number); err != nil {
		slog.Warn("error updating PR size", "number", p.number, "error", err)
		return false, nil
	}
	return true, nil
}

// markPRSizeZero writes zeros for unfetchable PRs (deleted, transferred, etc.)
// so they exit the "missing size" pool and don't block backfill progress.
func (e *eventImporter) markPRSizeZero(ctx context.Context, db DBTX, p prRef) (bool, error) {
	zero := intPtr(0)
	if _, err := db.ExecContext(ctx, updatePRSizeSQL, zero, zero, zero, zero,
		p.org, p.repo, p.number); err != nil {
		slog.Warn("error marking PR size as zero", "number", p.number, "error", err)
		return false, nil
	}
	return false, nil
}

func (e *eventImporter) importPRReviews(ctx context.Context, prNumber int) error {
	if prNumber == 0 {
		return nil
	}

	opts := &github.ListOptions{PerPage: pageSizeDefault}

	for {
		var reviews []*github.PullRequestReview
		var resp *github.Response
		if err := e.retryOnRateLimit(ctx, func() error {
			var apiErr error
			reviews, resp, apiErr = e.client.PullRequests.ListReviews(ctx, e.owner, e.repo, prNumber, opts)
			if apiErr != nil {
				return fmt.Errorf("error listing reviews for PR #%d: %w", prNumber, apiErr)
			}
			return ghutil.CheckRateLimit(ctx, resp)
		}); err != nil {
			return fmt.Errorf("listing PR reviews for #%d: %w", prNumber, err)
		}

		for i := range reviews {
			if reviews[i].User == nil || reviews[i].HTMLURL == nil {
				continue
			}
			n := prNumber
			extra := &eventExtra{
				Number:    &n,
				CreatedAt: timestampStr(reviews[i].SubmittedAt),
			}
			if err := e.add(ctx, data.EventTypePRReview, *reviews[i].HTMLURL, reviews[i].User,
				timestampToTime(reviews[i].SubmittedAt), nil, nil, extra); err != nil {
				return fmt.Errorf("error adding PR review event: %w", err)
			}
		}

		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}

	return nil
}

//nolint:dupl // pagination boilerplate shared with importPREvents/importForkEvents; different API + item processing
func (e *eventImporter) importIssueEvents(ctx context.Context) error {
	slog.Debug("starting issue event import", "page", e.state[data.EventTypeIssue].Page, "since", e.state[data.EventTypeIssue].Since.Format("2006-01-02"))

	opt := &github.IssueListByRepoOptions{
		State:     "all",
		Sort:      sortField,
		Direction: sortDirection,
		Since:     e.state[data.EventTypeIssue].Since,
		ListOptions: github.ListOptions{
			PerPage: pageSizeDefault,
			Page:    e.state[data.EventTypeIssue].Page,
		},
	}

	for {
		var items []*github.Issue
		var resp *github.Response
		if err := e.retryOnRateLimit(ctx, func() error {
			var apiErr error
			items, resp, apiErr = e.client.Issues.ListByRepo(ctx, e.owner, e.repo, opt)
			if apiErr != nil {
				return fmt.Errorf("error listing issues: %w", apiErr)
			}
			if resp.StatusCode != http.StatusOK {
				net.PrintHTTPResponse(resp.Response)
				return fmt.Errorf("error listing issues, rate: %s, status: %d", ghutil.RateInfo(&resp.Rate), resp.StatusCode)
			}
			return ghutil.CheckRateLimit(ctx, resp)
		}); err != nil {
			return fmt.Errorf("listing issues: %w", err)
		}
		slog.Debug("issue events", "found", len(items), "next_page", resp.NextPage, "last_page", resp.LastPage, "rate", ghutil.RateInfo(&resp.Rate))

		if len(items) == 0 {
			break
		}

		for i := range items {
			mentions := ghutil.ParseUsers(items[i].Body)
			mentions = append(mentions, ghutil.GetUsernames(items[i].Assignee)...)
			mentions = append(mentions, ghutil.GetUsernames(items[i].Assignees...)...)
			extra := &eventExtra{
				State:     items[i].State,
				Number:    items[i].Number,
				CreatedAt: timestampStr(items[i].CreatedAt),
				ClosedAt:  timestampStr(items[i].ClosedAt),
				Title:     items[i].GetTitle(),
			}
			// Use CreatedAt — see PR-import comment above.
			if err := e.add(ctx, data.EventTypeIssue, *items[i].HTMLURL, items[i].User,
				timestampToTime(items[i].CreatedAt), mentions, ghutil.GetLabels(items[i].Labels), extra); err != nil {
				return fmt.Errorf("error adding issue event: %s/%s: %w", e.owner, e.repo, err)
			}
		}

		e.updatePage(data.EventTypeIssue, opt.ListOptions.Page)

		if resp.NextPage == 0 {
			break
		}

		opt.ListOptions.Page = resp.NextPage
	}

	return nil
}

func getStrPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

//nolint:dupl // pagination boilerplate shared with importPRReviewEvents; different API + item processing
func (e *eventImporter) importIssueCommentEvents(ctx context.Context) error {
	slog.Debug("starting issue comment event import", "page", e.state[data.EventTypeIssueComment].Page, "since", e.state[data.EventTypeIssueComment].Since.Format("2006-01-02"))

	opt := &github.IssueListCommentsOptions{
		Sort:      getStrPtr(sortField),
		Direction: getStrPtr(sortCommentField),
		Since:     &e.state[data.EventTypeIssueComment].Since,
		ListOptions: github.ListOptions{
			PerPage: pageSizeDefault,
			Page:    e.state[data.EventTypeIssueComment].Page,
		},
	}

	for {
		var items []*github.IssueComment
		var resp *github.Response
		if err := e.retryOnRateLimit(ctx, func() error {
			var apiErr error
			items, resp, apiErr = e.client.Issues.ListComments(ctx, e.owner, e.repo, nilNumber, opt)
			if apiErr != nil {
				return fmt.Errorf("error listing issue comments: %w", apiErr)
			}
			if resp.StatusCode != http.StatusOK {
				net.PrintHTTPResponse(resp.Response)
				return fmt.Errorf("error listing issue comments, rate: %s, status: %d", ghutil.RateInfo(&resp.Rate), resp.StatusCode)
			}
			return ghutil.CheckRateLimit(ctx, resp)
		}); err != nil {
			return fmt.Errorf("listing issue comments: %w", err)
		}
		slog.Debug("issue comment events", "found", len(items), "next_page", resp.NextPage, "last_page", resp.LastPage, "rate", ghutil.RateInfo(&resp.Rate))

		if len(items) == 0 {
			break
		}

		for i := range items {
			extra := &eventExtra{
				CreatedAt: timestampStr(items[i].CreatedAt),
			}
			if items[i].HTMLURL != nil {
				if n := parseIssueNumberFromURL(*items[i].HTMLURL); n > 0 {
					extra.Number = &n
				}
			}
			if err := e.add(ctx, data.EventTypeIssueComment, *items[i].HTMLURL, items[i].User, timestampToTime(items[i].UpdatedAt), ghutil.ParseUsers(items[i].Body), nil, extra); err != nil {
				return fmt.Errorf("error adding issue comment event: %s/%s: %w", e.owner, e.repo, err)
			}
		}

		e.updatePage(data.EventTypeIssueComment, opt.ListOptions.Page)

		if resp.NextPage == 0 {
			break
		}

		opt.ListOptions.Page = resp.NextPage
	}

	return nil
}

//nolint:dupl // pagination boilerplate shared with importIssueCommentEvents; different API + item processing
func (e *eventImporter) importPRReviewEvents(ctx context.Context) error {
	slog.Debug("starting pr review event import", "page", e.state[data.EventTypePRReview].Page, "since", e.state[data.EventTypePRReview].Since.Format("2006-01-02"))

	opt := &github.PullRequestListCommentsOptions{
		Sort:      sortField,
		Direction: sortCommentField,
		Since:     e.state[data.EventTypePRReview].Since,
		ListOptions: github.ListOptions{
			PerPage: pageSizeDefault,
			Page:    e.state[data.EventTypePRReview].Page,
		},
	}

	for {
		var items []*github.PullRequestComment
		var resp *github.Response
		if err := e.retryOnRateLimit(ctx, func() error {
			var apiErr error
			items, resp, apiErr = e.client.PullRequests.ListComments(ctx, e.owner, e.repo, nilNumber, opt)
			if apiErr != nil {
				return fmt.Errorf("error listing pr comments: %w", apiErr)
			}
			if resp.StatusCode != http.StatusOK {
				net.PrintHTTPResponse(resp.Response)
				return fmt.Errorf("error listing pr comments, rate: %s, status: %d", ghutil.RateInfo(&resp.Rate), resp.StatusCode)
			}
			return ghutil.CheckRateLimit(ctx, resp)
		}); err != nil {
			return fmt.Errorf("listing PR comments: %w", err)
		}
		slog.Debug("pr review events", "found", len(items), "next_page", resp.NextPage, "last_page", resp.LastPage, "rate", ghutil.RateInfo(&resp.Rate))

		if len(items) == 0 {
			break
		}

		for i := range items {
			extra := &eventExtra{
				CreatedAt: timestampStr(items[i].CreatedAt),
			}
			if items[i].PullRequestURL != nil {
				if n := parsePRNumberFromURL(*items[i].PullRequestURL); n > 0 {
					extra.Number = &n
				}
			}
			if err := e.add(ctx, data.EventTypePRReview, *items[i].HTMLURL, items[i].User, timestampToTime(items[i].UpdatedAt), ghutil.ParseUsers(items[i].Body), nil, extra); err != nil {
				return fmt.Errorf("error adding PR comment event: %s/%s: %w", e.owner, e.repo, err)
			}
		}

		e.updatePage(data.EventTypePRReview, opt.ListOptions.Page)

		if resp.NextPage == 0 {
			break
		}

		opt.ListOptions.Page = resp.NextPage
	}

	return nil
}

//nolint:dupl // pagination boilerplate shared with importPREvents/importIssueEvents; different API + item processing
func (e *eventImporter) importForkEvents(ctx context.Context) error {
	slog.Debug("starting fork event import", "page", e.state[data.EventTypeFork].Page, "since", e.state[data.EventTypeFork].Since.Format("2006-01-02"))

	opt := &github.RepositoryListForksOptions{
		Sort: sortForkField,
		ListOptions: github.ListOptions{
			PerPage: pageSizeDefault,
			Page:    e.state[data.EventTypeFork].Page,
		},
	}

	for {
		var items []*github.Repository
		var resp *github.Response
		if err := e.retryOnRateLimit(ctx, func() error {
			var apiErr error
			items, resp, apiErr = e.client.Repositories.ListForks(ctx, e.owner, e.repo, opt)
			if apiErr != nil {
				return fmt.Errorf("error listing forks: %w", apiErr)
			}
			if resp.StatusCode != http.StatusOK {
				net.PrintHTTPResponse(resp.Response)
				return fmt.Errorf("error listing forks, rate: %s, status: %d", ghutil.RateInfo(&resp.Rate), resp.StatusCode)
			}
			return ghutil.CheckRateLimit(ctx, resp)
		}); err != nil {
			return fmt.Errorf("listing forks: %w", err)
		}
		slog.Debug("fork events", "found", len(items), "next_page", resp.NextPage, "last_page", resp.LastPage, "rate", ghutil.RateInfo(&resp.Rate))

		if len(items) == 0 {
			break
		}

		for i := range items {
			if err := e.add(ctx, data.EventTypeFork, *items[i].HTMLURL, items[i].Owner, &items[i].UpdatedAt.Time, nil, items[i].Topics, nil); err != nil {
				return fmt.Errorf("error adding fork event: %s/%s: %w", e.owner, e.repo, err)
			}
		}

		e.updatePage(data.EventTypeFork, opt.ListOptions.Page)

		if resp.NextPage == 0 {
			break
		}

		opt.ListOptions.Page = resp.NextPage
	}

	return nil
}

func unique(slice []string) []string {
	keys := make(map[string]bool)
	list := []string{}
	for _, entry := range slice {
		u := strings.ReplaceAll(strings.TrimSpace(entry), "@", "")
		if _, value := keys[u]; !value {
			keys[u] = true
			list = append(list, u)
		}
	}
	return list
}
