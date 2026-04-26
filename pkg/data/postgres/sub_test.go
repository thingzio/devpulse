package postgres

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thingzio/devpulse/pkg/data"
)

func TestSaveAndApplyDeveloperSub(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)
	devs := []*data.Developer{
		{Username: "user1", FullName: "User One", Entity: "OLDNAME"},
		{Username: "user2", FullName: "User Two", Entity: "OLDNAME"},
	}
	require.NoError(t, store.SaveDevelopers(ctx, devs))

	sub, err := store.SaveAndApplyDeveloperSub(ctx, "entity", "OLDNAME", "NEWNAME")
	require.NoError(t, err)
	assert.Equal(t, int64(2), sub.Records)

	dev, err := store.GetDeveloper(ctx, "user1")
	require.NoError(t, err)
	assert.Equal(t, "NEWNAME", dev.Entity)
}

func TestSaveAndApplyDeveloperSub_InvalidProperty(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)
	_, err := store.SaveAndApplyDeveloperSub(ctx, "invalid_prop", "old", "new")
	assert.Error(t, err)
}

func TestSaveAndApplyDeveloperSub_NilDB(t *testing.T) {
	ctx := context.Background()
	s := &Store{db: nil}
	_, err := s.SaveAndApplyDeveloperSub(ctx, "entity", "old", "new")
	assert.Error(t, err)
}

func TestApplySubstitutions(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)
	devs := []*data.Developer{
		{Username: "u1", FullName: "U1", Entity: "OLD"},
	}
	require.NoError(t, store.SaveDevelopers(ctx, devs))

	_, err := store.SaveAndApplyDeveloperSub(ctx, "entity", "OLD", "NEW")
	require.NoError(t, err)

	// Change entity back to OLD to verify re-application
	devs[0].Entity = "OLD"
	require.NoError(t, store.SaveDevelopers(ctx, devs))

	subs, err := store.ApplySubstitutions(ctx)
	require.NoError(t, err)
	assert.NotEmpty(t, subs)

	dev, err := store.GetDeveloper(ctx, "u1")
	require.NoError(t, err)
	assert.Equal(t, "NEW", dev.Entity)
}

func TestApplySubstitutions_NilDB(t *testing.T) {
	ctx := context.Background()
	s := &Store{db: nil}
	_, err := s.ApplySubstitutions(ctx)
	assert.Error(t, err)
}

func TestDeveloperSubSQL(t *testing.T) {
	tests := []struct {
		prop string
		ok   bool
	}{
		{"entity", true},
		{"", false},
		{"username", false},
		{"DROP TABLE devpulse_developer; --", false},
		{"entity; DROP TABLE devpulse_developer", false},
	}
	for _, tt := range tests {
		t.Run(tt.prop, func(t *testing.T) {
			sql, ok := developerSubSQL(tt.prop)
			assert.Equal(t, tt.ok, ok)
			if tt.ok {
				assert.Equal(t, updateDeveloperEntityBatchSQL, sql)
			} else {
				assert.Empty(t, sql)
			}
		})
	}
}
