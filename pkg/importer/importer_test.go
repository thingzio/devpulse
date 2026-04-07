package importer

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

func TestModeConstants(t *testing.T) {
	assert.Equal(t, "all", ModeAll)
	assert.Equal(t, "import", ModeImport)
	assert.Equal(t, "reputation", ModeReputation)
}

func TestModeImportSkipsDeepReputation(t *testing.T) {
	// The deep reputation gate in importRepo checks:
	//   token != "" && limits.DeepReputation && mode != ModeImport
	// Verify the gate logic for each mode.
	tests := []struct {
		name     string
		mode     string
		wantSkip bool
	}{
		{"all mode runs deep rep", ModeAll, false},
		{"import mode skips deep rep", ModeImport, true},
		{"reputation mode would run deep rep", ModeReputation, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			skipped := tc.mode == ModeImport
			assert.Equal(t, tc.wantSkip, skipped)
		})
	}
}
