package server

import (
	"context"
	"testing"

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
