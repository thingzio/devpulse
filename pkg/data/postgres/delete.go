// Copyright 2026 Thingz LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
// SPDX-License-Identifier: Apache-2.0

package postgres

import (
	"context"
	"fmt"

	"github.com/thingzio/devpulse/pkg/data"
)

const (
	deleteReleaseAssetsSQL = `DELETE FROM devpulse_release_asset WHERE org = $1 AND repo = $2`
	deleteReleasesSQL      = `DELETE FROM devpulse_release WHERE org = $1 AND repo = $2`
	deleteEventsSQL        = `DELETE FROM devpulse_event WHERE org = $1 AND repo = $2`
	deleteRepoMetaSQL      = `DELETE FROM devpulse_repo_meta WHERE org = $1 AND repo = $2`
	deleteStateSQL         = `DELETE FROM devpulse_state WHERE org = $1 AND repo = $2`
)

func (s *Store) DeleteRepoData(ctx context.Context, org, repo string) (*data.DeleteResult, error) {
	if s.db == nil {
		return nil, data.ErrDBNotInitialized
	}

	if org == "" || repo == "" {
		return nil, fmt.Errorf("org and repo are required (got org=%q, repo=%q)", org, repo)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("beginning delete transaction: %w", err)
	}
	defer rollbackTransaction(tx)

	result := &data.DeleteResult{Org: org, Repo: repo}

	deletes := []struct {
		sql   string
		field *int64
	}{
		{deleteReleaseAssetsSQL, &result.ReleaseAssets},
		{deleteReleasesSQL, &result.Releases},
		{deleteEventsSQL, &result.Events},
		{deleteRepoMetaSQL, &result.RepoMeta},
		{deleteStateSQL, &result.State},
	}

	for _, d := range deletes {
		res, execErr := tx.ExecContext(ctx, d.sql, org, repo)
		if execErr != nil {
			return nil, fmt.Errorf("deleting from %s/%s: %w", org, repo, execErr)
		}
		n, raErr := res.RowsAffected()
		if raErr != nil {
			return nil, fmt.Errorf("getting rows affected: %w", raErr)
		}
		*d.field = n
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("committing delete transaction: %w", err)
	}

	return result, nil
}
