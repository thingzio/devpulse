package server

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestTokenQuotaSample_Struct(t *testing.T) {
	now := time.Now()
	s := tokenQuotaSample{
		SampledAt:      now,
		InstallationID: 42,
		Login:          "acme",
		QuotaLimit:     5000,
		QuotaUsed:      1200,
	}

	assert.Equal(t, int64(42), s.InstallationID)
	assert.Equal(t, "acme", s.Login)
	assert.Equal(t, 5000, s.QuotaLimit)
	assert.Equal(t, 1200, s.QuotaUsed)
	assert.Equal(t, now, s.SampledAt)
}
