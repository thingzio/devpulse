package postgres

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunSaaSMigrations(t *testing.T) {
	store := setupTestDB(t)
	db := store.DB()

	err := RunSaaSMigrations(db)
	require.NoError(t, err)

	// Verify tenant table exists
	var exists bool
	err = db.QueryRow(`SELECT EXISTS (
		SELECT 1 FROM information_schema.tables
		WHERE table_name = 'tenant' AND table_schema = current_schema()
	)`).Scan(&exists)
	require.NoError(t, err)
	assert.True(t, exists, "tenant table should exist")

	// Verify session table exists
	err = db.QueryRow(`SELECT EXISTS (
		SELECT 1 FROM information_schema.tables
		WHERE table_name = 'session' AND table_schema = current_schema()
	)`).Scan(&exists)
	require.NoError(t, err)
	assert.True(t, exists, "session table should exist")

	// Verify idempotency
	err = RunSaaSMigrations(db)
	require.NoError(t, err)
}
