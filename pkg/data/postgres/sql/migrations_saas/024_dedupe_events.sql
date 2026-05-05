-- Dedupe PR/Issue event rows that accumulated under the old UpdatedAt-based
-- date key. Rebuilds each (org, repo, type, number) as a single row whose
-- `date` column is created_at::date — matching what post-fix code emits, so
-- future imports upsert into the existing row instead of inserting again.

-- Step 1 — entity winners: one row per (org, repo, type, number), preferring
-- terminal state (merged/closed), then rows with merged_at/closed_at set,
-- then the most-recent date.
CREATE TEMP TABLE event_entity_winners ON COMMIT DROP AS
SELECT DISTINCT ON (org, repo, type, number)
    org, repo, username, type,
    COALESCE(TO_CHAR(created_at::timestamp, 'YYYY-MM-DD'), date) AS date,
    url, mentions, labels, state, number, created_at, closed_at, merged_at,
    additions, deletions, changed_files, commits, title
FROM devpulse_event
WHERE type IN ('pr', 'issue') AND number IS NOT NULL
ORDER BY org, repo, type, number,
    (state IN ('merged', 'closed'))::int DESC,
    (merged_at IS NOT NULL)::int DESC,
    (closed_at IS NOT NULL)::int DESC,
    date DESC NULLS LAST;

-- Step 2 — collapse cross-PR collisions: one author can open multiple PRs
-- on the same day, which now share (org, repo, username, type, date). Keep
-- the row with the most-definitive end-state. Reduces input to the final
-- INSERT to be unique on the PK so ON CONFLICT isn't needed.
CREATE TEMP TABLE event_final_winners ON COMMIT DROP AS
SELECT DISTINCT ON (org, repo, username, type, date)
    org, repo, username, type, date,
    url, mentions, labels, state, number, created_at, closed_at, merged_at,
    additions, deletions, changed_files, commits, title
FROM event_entity_winners
ORDER BY org, repo, username, type, date,
    (state IN ('merged', 'closed'))::int DESC,
    (merged_at IS NOT NULL)::int DESC,
    (closed_at IS NOT NULL)::int DESC,
    number DESC;

-- Step 3 — wipe affected rows. Other event types (issue_comment, pr_review,
-- fork) and rows without a number are untouched.
DELETE FROM devpulse_event WHERE type IN ('pr', 'issue') AND number IS NOT NULL;

-- Step 4 — reinsert the deduped winners.
INSERT INTO devpulse_event (
    org, repo, username, type, date,
    url, mentions, labels, state, number, created_at, closed_at, merged_at,
    additions, deletions, changed_files, commits, title
)
SELECT
    org, repo, username, type, date,
    url, mentions, labels, state, number, created_at, closed_at, merged_at,
    additions, deletions, changed_files, commits, title
FROM event_final_winners;
