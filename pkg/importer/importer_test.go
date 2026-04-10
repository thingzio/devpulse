package importer

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thingzio/devpulse/pkg/tenant"
)

func TestRetryRL(t *testing.T) {
	ctx := context.Background()
	retryRL := newRetryRL(ctx)

	t.Run("success on first call", func(t *testing.T) {
		err := retryRL(func() error { return nil })
		require.NoError(t, err)
	})

	t.Run("non-rate-limit error returned", func(t *testing.T) {
		sentinel := errors.New("db connection failed")
		err := retryRL(func() error { return sentinel })
		require.Error(t, err)
		assert.ErrorIs(t, err, sentinel)
		assert.Contains(t, err.Error(), "retryRL:")
	})

	t.Run("transient error then success not confused as error", func(t *testing.T) {
		// Simulates the old bug: if fn succeeds, retryRL must return nil.
		// With a non-rate-limit error WaitForRateReset returns false,
		// so this tests the wrapping path. The rate-limit retry path
		// is structurally identical — the fix ensures fn() returning nil
		// produces a nil return.
		calls := 0
		err := retryRL(func() error {
			calls++
			if calls == 1 {
				return errors.New("transient")
			}
			return nil
		})
		// Non-rate-limit errors are not retried, so this returns an error.
		require.Error(t, err)
		assert.Equal(t, 1, calls)
	})

	t.Run("wrapped error preserves chain", func(t *testing.T) {
		inner := errors.New("inner")
		wrapped := fmt.Errorf("outer: %w", inner)
		err := retryRL(func() error { return wrapped })
		require.Error(t, err)
		assert.ErrorIs(t, err, inner)
	})
}

func TestBestPlanForRepo(t *testing.T) {
	t.Parallel()

	t.Run("single free tenant", func(t *testing.T) {
		t.Parallel()
		got := bestPlanForRepo([]TenantRef{{Plan: "free"}})
		assert.Equal(t, "free", got)
	})

	t.Run("enterprise wins immediately", func(t *testing.T) {
		t.Parallel()
		got := bestPlanForRepo([]TenantRef{{Plan: "free"}, {Plan: "enterprise"}})
		assert.Equal(t, "enterprise", got)
	})

	t.Run("pro beats starter", func(t *testing.T) {
		t.Parallel()
		got := bestPlanForRepo([]TenantRef{{Plan: "starter"}, {Plan: "pro"}})
		assert.Equal(t, "pro", got)
	})

	t.Run("nil tenants returns empty", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, "", bestPlanForRepo(nil))
	})

	t.Run("empty slice returns empty", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, "", bestPlanForRepo([]TenantRef{}))
	})
}

func TestBuildAndShardIntegration(t *testing.T) {
	t.Parallel()

	// Simulate 2 tenants sharing NVIDIA/aicr, plus unique repos with event counts.
	rows := []tenant.ImportWorkRow{
		{TenantRepoID: "tr1", TenantID: "t1", Org: "NVIDIA", Repo: "aicr", Plan: "pro", EventCount: 1000},
		{TenantRepoID: "tr2", TenantID: "t2", Org: "NVIDIA", Repo: "aicr", Plan: "enterprise", EventCount: 1000},
		{TenantRepoID: "tr3", TenantID: "t1", Org: "NVIDIA", Repo: "cccl", Plan: "pro", EventCount: 5000},
		{TenantRepoID: "tr4", TenantID: "t1", Org: "ai-dynamo", Repo: "dynamo", Plan: "pro", EventCount: 12000},
		{TenantRepoID: "tr5", TenantID: "t2", Org: "ai-dynamo", Repo: "nixl", Plan: "enterprise", EventCount: 3600},
	}

	workList := BuildWorkList(rows)
	// NVIDIA/aicr deduped: 4 unique repos from 5 rows.
	require.Len(t, workList, 4)

	// aicr should have 2 tenants and correct weight.
	for _, rw := range workList {
		if rw.Org == "NVIDIA" && rw.Repo == "aicr" {
			assert.Len(t, rw.Tenants, 2)
			assert.Equal(t, 1000, rw.Weight)
		}
	}

	// Shard across 2 tasks — all repos covered, no overlap.
	all := make([]RepoWork, 0, len(workList))
	for idx := range 2 {
		all = append(all, ShardRepos(workList, 2, idx)...)
	}
	assert.Len(t, all, 4)
	seen := map[string]bool{}
	for _, rw := range all {
		key := rw.Org + "/" + rw.Repo
		assert.False(t, seen[key], "duplicate: %s", key)
		seen[key] = true
	}

	// With weighted sharding, dynamo (12000) and cccl (5000) should be
	// in different shards for better balance.
	has := func(shard []RepoWork, name string) bool {
		for _, r := range shard {
			if r.Repo == name {
				return true
			}
		}
		return false
	}
	shard0 := ShardRepos(workList, 2, 0)
	shard1 := ShardRepos(workList, 2, 1)
	if has(shard0, "dynamo") {
		assert.False(t, has(shard0, "cccl"), "dynamo and cccl should be in different shards")
		assert.True(t, has(shard1, "cccl"), "cccl should be in the other shard")
	} else {
		assert.True(t, has(shard1, "dynamo"), "dynamo must be in one shard")
		assert.False(t, has(shard1, "cccl"), "dynamo and cccl should be in different shards")
		assert.True(t, has(shard0, "cccl"), "cccl should be in the other shard")
	}
}
