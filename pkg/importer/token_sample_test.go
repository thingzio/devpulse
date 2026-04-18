package importer

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestPurgeThreshold(t *testing.T) {
	threshold := purgeThreshold()
	expected := time.Now().UTC().AddDate(0, 0, -quotaSampleRetentionDays)
	assert.WithinDuration(t, expected, threshold, 2*time.Second)
}

func TestSampleTokenQuotas_NilConfig(t *testing.T) {
	// Should be a no-op when ghAppConfig is nil — no panic, no error.
	sampleTokenQuotas(context.Background(), nil, nil)
}
