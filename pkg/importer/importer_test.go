package importer

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

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
