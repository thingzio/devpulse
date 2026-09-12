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

package importer

import (
	"sort"
	"strings"

	"github.com/thingzio/devpulse/pkg/tenant"
)

// RepoWork represents a unique repo to import, with all tenants that track it.
type RepoWork struct {
	Org     string
	Repo    string
	Weight  int // event count for sharding weight; 0 = new repo
	Tenants []TenantRef
}

// TenantRef links a tenant to their tenant_repo row for post-import bookkeeping.
type TenantRef struct {
	TenantRepoID string
	TenantID     string
	Plan         string
}

// BuildWorkList deduplicates tenant_repo rows by (org, repo), grouping
// tenant refs under each unique repo. Preserves exact org/repo casing.
func BuildWorkList(rows []tenant.ImportWorkRow) []RepoWork {
	if len(rows) == 0 {
		return nil
	}

	idx := map[string]int{} // "org/repo" → index in result
	var result []RepoWork

	for _, r := range rows {
		key := r.Org + "/" + r.Repo
		if i, ok := idx[key]; ok {
			result[i].Tenants = append(result[i].Tenants, TenantRef{
				TenantRepoID: r.TenantRepoID,
				TenantID:     r.TenantID,
				Plan:         r.Plan,
			})
			continue
		}
		idx[key] = len(result)
		result = append(result, RepoWork{
			Org:    r.Org,
			Repo:   r.Repo,
			Weight: r.EventCount,
			Tenants: []TenantRef{{
				TenantRepoID: r.TenantRepoID,
				TenantID:     r.TenantID,
				Plan:         r.Plan,
			}},
		})
	}
	return result
}

// ShardRepos deterministically assigns repos to a task shard using greedy
// bin-packing by weight. Heavy repos are spread across tasks to balance
// cumulative import cost. Ties broken by lower task index for determinism.
// Guarantees: union of all shards == input, no gaps, no overlaps.
func ShardRepos(repos []RepoWork, taskCount, taskIndex int) []RepoWork {
	if len(repos) == 0 || taskCount < 1 {
		return nil
	}

	// Sort by weight descending; break ties by lowercase(repo), lowercase(org)
	// for determinism.
	sorted := make([]RepoWork, len(repos))
	copy(sorted, repos)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Weight != sorted[j].Weight {
			return sorted[i].Weight > sorted[j].Weight
		}
		ri, rj := strings.ToLower(sorted[i].Repo), strings.ToLower(sorted[j].Repo)
		if ri != rj {
			return ri < rj
		}
		return strings.ToLower(sorted[i].Org) < strings.ToLower(sorted[j].Org)
	})

	// Greedy assignment: each repo goes to the task with the lowest
	// cumulative weight. Ties broken by lower task index for determinism.
	taskWeights := make([]int, taskCount)
	assignments := make([]int, len(sorted))
	for i, r := range sorted {
		minTask := 0
		for t := 1; t < taskCount; t++ {
			if taskWeights[t] < taskWeights[minTask] {
				minTask = t
			}
		}
		assignments[i] = minTask
		taskWeights[minTask] += max(r.Weight, 1) // zero-weight = 1 to avoid starvation
	}

	var shard []RepoWork
	for i, r := range sorted {
		if assignments[i] == taskIndex {
			shard = append(shard, r)
		}
	}
	return shard
}
