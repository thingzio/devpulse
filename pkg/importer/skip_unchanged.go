package importer

import "time"

// shouldSkipUnchangedRepo returns true when the repo has not been pushed to
// since the most recent event we already imported. Both timestamps must be
// non-zero for a skip; zero means "unknown" and we always import.
func shouldSkipUnchangedRepo(pushedAt, maxEventTime time.Time) bool {
	if pushedAt.IsZero() || maxEventTime.IsZero() {
		return false
	}
	return !pushedAt.After(maxEventTime)
}
