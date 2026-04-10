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

// ShardRepos deterministically assigns repos to a task shard.
// Sorts by lowercase(repo), then lowercase(org) as tiebreaker, then
// assigns every taskCount-th item to the given taskIndex.
// Guarantees: union of all shards == input, no gaps, no overlaps.
func ShardRepos(repos []RepoWork, taskCount, taskIndex int) []RepoWork {
	if len(repos) == 0 || taskCount < 1 {
		return nil
	}

	sorted := make([]RepoWork, len(repos))
	copy(sorted, repos)
	sort.SliceStable(sorted, func(i, j int) bool {
		ri, rj := strings.ToLower(sorted[i].Repo), strings.ToLower(sorted[j].Repo)
		if ri != rj {
			return ri < rj
		}
		return strings.ToLower(sorted[i].Org) < strings.ToLower(sorted[j].Org)
	})

	var shard []RepoWork
	for i, r := range sorted {
		if i%taskCount == taskIndex {
			shard = append(shard, r)
		}
	}
	return shard
}
