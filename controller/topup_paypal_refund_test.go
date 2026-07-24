package controller

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParsePayPalRefundUSD(t *testing.T) {
	cases := []struct {
		name     string
		currency string
		value    string
		want     float64
		wantErr  bool
	}{
		{"full50", "USD", "50.00", 50, false},
		{"emptyCurrencyOK", "", "2.00", 2, false},
		{"nonUSD", "EUR", "50.00", 0, true},
		{"zero", "USD", "0.00", 0, true},
		{"negative", "USD", "-5.00", 0, true},
		{"garbage", "USD", "abc", 0, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := parsePayPalRefundUSD(c.currency, c.value)
			if c.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.InDelta(t, c.want, got, 0.0001)
		})
	}
}
