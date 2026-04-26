package server

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestNewImportTrigger_EmptyJobName(t *testing.T) {
	trigger, err := newImportTrigger(context.Background(), "")
	assert.NoError(t, err)
	assert.Nil(t, trigger)
}

func TestImportTrigger_NilSafe(t *testing.T) {
	var trigger *importTrigger
	err := trigger.TriggerRepoImport(context.Background(), "org", "repo")
	assert.NoError(t, err)
}

func TestImportTrigger_Close_NilSafe(t *testing.T) {
	var trigger *importTrigger
	err := trigger.Close()
	assert.NoError(t, err)
}

func TestImportTrigger_AsyncAndWait_NilSafe(t *testing.T) {
	var trigger *importTrigger
	// Should be a no-op and not panic.
	trigger.TriggerRepoImportAsync("org", "repo", time.Second)
	trigger.Wait()
}
