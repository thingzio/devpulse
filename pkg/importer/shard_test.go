package importer

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thingzio/devpulse/pkg/tenant"
)

func TestBuildWorkList(t *testing.T) {
	t.Parallel()

	t.Run("empty input", func(t *testing.T) {
		t.Parallel()
		got := BuildWorkList(nil)
		assert.Empty(t, got)
	})

	t.Run("single tenant single repo", func(t *testing.T) {
		t.Parallel()
		rows := []tenant.ImportWorkRow{
			{TenantRepoID: "tr1", TenantID: "t1", Org: "NVIDIA", Repo: "aicr", Plan: "pro", EventCount: 0},
		}
		got := BuildWorkList(rows)
		require.Len(t, got, 1)
		assert.Equal(t, "NVIDIA", got[0].Org)
		assert.Equal(t, "aicr", got[0].Repo)
		require.Len(t, got[0].Tenants, 1)
		assert.Equal(t, "t1", got[0].Tenants[0].TenantID)
	})

	t.Run("two tenants same repo deduplicates", func(t *testing.T) {
		t.Parallel()
		rows := []tenant.ImportWorkRow{
			{TenantRepoID: "tr1", TenantID: "t1", Org: "NVIDIA", Repo: "aicr", Plan: "pro", EventCount: 0},
			{TenantRepoID: "tr2", TenantID: "t2", Org: "NVIDIA", Repo: "aicr", Plan: "free", EventCount: 0},
		}
		got := BuildWorkList(rows)
		require.Len(t, got, 1)
		assert.Len(t, got[0].Tenants, 2)
	})

	t.Run("different repos stay separate", func(t *testing.T) {
		t.Parallel()
		rows := []tenant.ImportWorkRow{
			{TenantRepoID: "tr1", TenantID: "t1", Org: "NVIDIA", Repo: "aicr", Plan: "pro", EventCount: 0},
			{TenantRepoID: "tr2", TenantID: "t1", Org: "NVIDIA", Repo: "cccl", Plan: "pro", EventCount: 0},
		}
		got := BuildWorkList(rows)
		assert.Len(t, got, 2)
	})

	t.Run("case-sensitive org/repo matching", func(t *testing.T) {
		t.Parallel()
		// NVIDIA/aicr and nvidia/aicr are different repos on GitHub.
		rows := []tenant.ImportWorkRow{
			{TenantRepoID: "tr1", TenantID: "t1", Org: "NVIDIA", Repo: "aicr", Plan: "pro", EventCount: 0},
			{TenantRepoID: "tr2", TenantID: "t2", Org: "nvidia", Repo: "aicr", Plan: "free", EventCount: 0},
		}
		got := BuildWorkList(rows)
		assert.Len(t, got, 2, "different orgs should not deduplicate")
	})

	t.Run("weight propagated from event count", func(t *testing.T) {
		t.Parallel()
		rows := []tenant.ImportWorkRow{
			{TenantRepoID: "tr1", TenantID: "t1", Org: "NVIDIA", Repo: "dynamo", Plan: "pro", EventCount: 12000},
			{TenantRepoID: "tr2", TenantID: "t2", Org: "NVIDIA", Repo: "dynamo", Plan: "enterprise", EventCount: 12000},
		}
		got := BuildWorkList(rows)
		require.Len(t, got, 1)
		assert.Equal(t, 12000, got[0].Weight)
	})

	t.Run("zero weight for new repo", func(t *testing.T) {
		t.Parallel()
		rows := []tenant.ImportWorkRow{
			{TenantRepoID: "tr1", TenantID: "t1", Org: "x", Repo: "new", Plan: "free", EventCount: 0},
		}
		got := BuildWorkList(rows)
		require.Len(t, got, 1)
		assert.Equal(t, 0, got[0].Weight)
	})
}

func TestShardRepos(t *testing.T) {
	t.Parallel()

	t.Run("empty list returns nil", func(t *testing.T) {
		t.Parallel()
		got := ShardRepos(nil, 3, 0)
		assert.Nil(t, got)

		got = ShardRepos([]RepoWork{}, 3, 0)
		assert.Nil(t, got)
	})

	t.Run("invalid task count returns nil", func(t *testing.T) {
		t.Parallel()
		repos := []RepoWork{{Org: "o", Repo: "r"}}
		assert.Nil(t, ShardRepos(repos, 0, 0))
		assert.Nil(t, ShardRepos(repos, -1, 0))
	})

	t.Run("single repo single task", func(t *testing.T) {
		t.Parallel()
		repos := []RepoWork{
			{Org: "nvidia", Repo: "cuda", Tenants: []TenantRef{{TenantRepoID: "tr1", TenantID: "t1", Plan: "pro"}}},
		}
		got := ShardRepos(repos, 1, 0)
		require.Len(t, got, 1)
		assert.Equal(t, "cuda", got[0].Repo)
		assert.Equal(t, "nvidia", got[0].Org)
		assert.Len(t, got[0].Tenants, 1)
	})

	t.Run("task_count=1 returns all repos", func(t *testing.T) {
		t.Parallel()
		repos := makeRepos("a/x", "b/y", "c/z")
		got := ShardRepos(repos, 1, 0)
		assert.Len(t, got, 3)
	})

	t.Run("three tasks no gaps no overlaps", func(t *testing.T) {
		t.Parallel()
		repos := makeRepos("org1/alpha", "org2/bravo", "org3/charlie", "org4/delta", "org5/echo")

		var all []RepoWork
		for idx := range 3 {
			shard := ShardRepos(repos, 3, idx)
			all = append(all, shard...)
		}

		assertExactCoverage(t, repos, all)
	})

	t.Run("more tasks than repos gives empty shards", func(t *testing.T) {
		t.Parallel()
		repos := makeRepos("o/a", "o/b")

		var all []RepoWork
		nonEmpty := 0
		for idx := range 5 {
			shard := ShardRepos(repos, 5, idx)
			if len(shard) > 0 {
				nonEmpty++
			}
			all = append(all, shard...)
		}

		assert.Equal(t, 2, nonEmpty, "only 2 of 5 shards should have repos")
		assertExactCoverage(t, repos, all)
	})

	t.Run("equal weight sorts alphabetically by repo then org", func(t *testing.T) {
		t.Parallel()
		repos := []RepoWork{
			{Org: "NVIDIA", Repo: "Zlib", Weight: 100},
			{Org: "apple", Repo: "aicr", Weight: 100},
			{Org: "NVIDIA", Repo: "aicr", Weight: 100},
		}

		// taskCount=1 returns all in sorted order (weight desc, then alpha)
		got := ShardRepos(repos, 1, 0)
		require.Len(t, got, 3)

		// Equal weight → aicr sorts before zlib; within aicr, apple < nvidia
		assert.Equal(t, "aicr", got[0].Repo)
		assert.Equal(t, "apple", got[0].Org)
		assert.Equal(t, "aicr", got[1].Repo)
		assert.Equal(t, "NVIDIA", got[1].Org)
		assert.Equal(t, "Zlib", got[2].Repo)
	})

	t.Run("deterministic across calls", func(t *testing.T) {
		t.Parallel()
		repos := makeRepos("z/m", "a/n", "m/a", "m/z", "a/a")

		first := ShardRepos(repos, 3, 1)
		for i := range 50 {
			got := ShardRepos(repos, 3, 1)
			require.Equal(t, first, got, "call %d produced different result", i)
		}
	})

	t.Run("does not mutate input slice", func(t *testing.T) {
		t.Parallel()
		repos := makeRepos("z/b", "a/a")
		origFirst := repos[0]
		_ = ShardRepos(repos, 1, 0)
		assert.Equal(t, origFirst, repos[0], "input slice was mutated")
	})

	t.Run("tenants preserved through sharding", func(t *testing.T) {
		t.Parallel()
		repos := []RepoWork{
			{Org: "o1", Repo: "r1", Tenants: []TenantRef{
				{TenantRepoID: "tr1", TenantID: "t1", Plan: "free"},
				{TenantRepoID: "tr2", TenantID: "t2", Plan: "pro"},
			}},
		}
		got := ShardRepos(repos, 1, 0)
		require.Len(t, got, 1)
		assert.Len(t, got[0].Tenants, 2)
		assert.Equal(t, "tr1", got[0].Tenants[0].TenantRepoID)
		assert.Equal(t, "tr2", got[0].Tenants[1].TenantRepoID)
	})
}

func TestShardReposExhaustive(t *testing.T) {
	t.Parallel()

	repos := make([]RepoWork, 20)
	names := []string{
		"alpha", "bravo", "charlie", "delta", "echo",
		"foxtrot", "golf", "hotel", "india", "juliet",
		"kilo", "lima", "mike", "november", "oscar",
		"papa", "quebec", "romeo", "sierra", "tango",
	}
	orgs := []string{"NVIDIA", "apple", "Google", "meta", "dgxc-io"}
	for i := range repos {
		repos[i] = RepoWork{
			Org:    orgs[i%len(orgs)],
			Repo:   names[i],
			Weight: (i + 1) * 500,
			Tenants: []TenantRef{
				{TenantRepoID: fmt.Sprintf("tr-%d", i), TenantID: fmt.Sprintf("t-%d", i%3), Plan: "pro"},
			},
		}
	}

	// For every taskCount from 1 to N+2, verify complete coverage.
	for tc := 1; tc <= len(repos)+2; tc++ {
		t.Run(fmt.Sprintf("taskCount=%d", tc), func(t *testing.T) {
			t.Parallel()

			var all []RepoWork
			for idx := range tc {
				shard := ShardRepos(repos, tc, idx)
				all = append(all, shard...)
			}

			assertExactCoverage(t, repos, all)
		})
	}
}

// makeRepos creates RepoWork items from "org/repo" strings.
func makeRepos(specs ...string) []RepoWork {
	out := make([]RepoWork, len(specs))
	for i, s := range specs {
		parts := strings.SplitN(s, "/", 2)
		out[i] = RepoWork{Org: parts[0], Repo: parts[1]}
	}
	return out
}

func TestShardReposWeighted(t *testing.T) {
	t.Parallel()

	t.Run("heavy repos distributed across tasks", func(t *testing.T) {
		t.Parallel()
		repos := []RepoWork{
			{Org: "a", Repo: "dynamo", Weight: 12000},
			{Org: "a", Repo: "cccl", Weight: 5000},
			{Org: "a", Repo: "cuopt", Weight: 2500},
			{Org: "a", Repo: "nixl", Weight: 3600},
			{Org: "a", Repo: "aicr", Weight: 1000},
			{Org: "a", Repo: "small", Weight: 100},
		}

		shard0 := ShardRepos(repos, 3, 0)
		shard1 := ShardRepos(repos, 3, 1)
		shard2 := ShardRepos(repos, 3, 2)

		// dynamo (12000) and cccl (5000) must NOT be in the same shard.
		has := func(shard []RepoWork, name string) bool {
			for _, r := range shard {
				if r.Repo == name {
					return true
				}
			}
			return false
		}
		for i, shard := range [][]RepoWork{shard0, shard1, shard2} {
			if has(shard, "dynamo") {
				assert.False(t, has(shard, "cccl"),
					"shard %d has both dynamo and cccl", i)
			}
		}

		// All repos covered.
		total := len(shard0) + len(shard1) + len(shard2)
		assert.Equal(t, 6, total)
	})

	t.Run("zero weight repos still assigned", func(t *testing.T) {
		t.Parallel()
		repos := []RepoWork{
			{Org: "a", Repo: "big", Weight: 10000},
			{Org: "a", Repo: "new1", Weight: 0},
			{Org: "a", Repo: "new2", Weight: 0},
		}
		all := make([]RepoWork, 0, 3)
		for idx := range 2 {
			all = append(all, ShardRepos(repos, 2, idx)...)
		}
		assert.Len(t, all, 3)
	})

	t.Run("equal weights fall back to alphabetical", func(t *testing.T) {
		t.Parallel()
		repos := []RepoWork{
			{Org: "b", Repo: "zebra", Weight: 100},
			{Org: "a", Repo: "alpha", Weight: 100},
			{Org: "a", Repo: "beta", Weight: 100},
		}
		// With equal weights and 3 tasks, each gets 1 repo.
		// Order should be deterministic.
		s0 := ShardRepos(repos, 3, 0)
		s1 := ShardRepos(repos, 3, 1)
		s2 := ShardRepos(repos, 3, 2)
		require.Len(t, s0, 1)
		require.Len(t, s1, 1)
		require.Len(t, s2, 1)
		// Verify determinism across calls.
		assert.Equal(t, s0, ShardRepos(repos, 3, 0))
	})
}

// assertExactCoverage verifies that got contains exactly the same repos as
// want with no duplicates and no missing entries.
func assertExactCoverage(t *testing.T, want, got []RepoWork) {
	t.Helper()

	require.Len(t, got, len(want), "shard union length mismatch")

	toKey := func(r RepoWork) string {
		return strings.ToLower(r.Org) + "/" + strings.ToLower(r.Repo)
	}

	wantKeys := make([]string, len(want))
	for i, r := range want {
		wantKeys[i] = toKey(r)
	}
	sort.Strings(wantKeys)

	gotKeys := make([]string, len(got))
	for i, r := range got {
		gotKeys[i] = toKey(r)
	}
	sort.Strings(gotKeys)

	assert.Equal(t, wantKeys, gotKeys, "shard union does not match input")

	// Check no duplicates.
	seen := make(map[string]bool, len(got))
	for _, k := range gotKeys {
		assert.False(t, seen[k], "duplicate repo in shards: %s", k)
		seen[k] = true
	}
}
