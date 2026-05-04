package importer

import "time"

// shouldSkipUnchangedRepo returns true when the repo has not been pushed to
// since the most recent event we already imported. Both timestamps must be
// non-zero for a skip; zero means "unknown" and we always import.
//
// hasForkData must be true for a skip — when fork events are missing for a
// repo we always re-import, even if pushed_at hasn't moved. This recovers
// from migration-025 (fork wipe) when MAX(created_at) on remaining PR/issue
// rows races ahead of pushed_at and would otherwise trap the repo in a
// skip loop indefinitely.
func shouldSkipUnchangedRepo(pushedAt, maxEventTime time.Time, hasForkData bool) bool {
	if pushedAt.IsZero() || maxEventTime.IsZero() {
		return false
	}
	if !hasForkData {
		return false
	}
	return !pushedAt.After(maxEventTime)
}
