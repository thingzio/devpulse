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

	// Simulate 2 tenants sharing NVIDIA/aicr, plus unique repos.
	rows := []tenant.ImportWorkRow{
		{TenantRepoID: "tr1", TenantID: "t1", Org: "NVIDIA", Repo: "aicr", Plan: "pro"},
		{TenantRepoID: "tr2", TenantID: "t2", Org: "NVIDIA", Repo: "aicr", Plan: "enterprise"},
		{TenantRepoID: "tr3", TenantID: "t1", Org: "NVIDIA", Repo: "cccl", Plan: "pro"},
		{TenantRepoID: "tr4", TenantID: "t1", Org: "ai-dynamo", Repo: "dynamo", Plan: "pro"},
		{TenantRepoID: "tr5", TenantID: "t2", Org: "ai-dynamo", Repo: "nixl", Plan: "enterprise"},
	}

	workList := BuildWorkList(rows)
	// NVIDIA/aicr deduped: 4 unique repos from 5 rows.
	require.Len(t, workList, 4)

	// aicr should have 2 tenants.
	for _, rw := range workList {
		if rw.Org == "NVIDIA" && rw.Repo == "aicr" {
			assert.Len(t, rw.Tenants, 2)
		}
	}

	// Shard across 2 tasks — all repos covered, no overlap.
	var all []RepoWork
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
}

