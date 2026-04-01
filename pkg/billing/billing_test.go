package billing

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPriceIDForPeriod(t *testing.T) {
	cfg := &Config{
		MonthlyPriceID: "price_monthly",
		AnnualPriceID:  "price_annual",
	}

	tests := []struct {
		period  string
		want    string
		wantErr bool
	}{
		{"monthly", "price_monthly", false},
		{"annual", "price_annual", false},
		{"weekly", "", true},
		{"", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.period, func(t *testing.T) {
			got, err := cfg.PriceIDForPeriod(tt.period)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestLoadConfig_Disabled(t *testing.T) {
	t.Setenv("STRIPE_SECRET_KEY", "")
	cfg := LoadConfig()
	assert.Nil(t, cfg)
}
