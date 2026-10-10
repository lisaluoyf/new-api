package service

import (
	"context"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
	"math"
	"testing"
)

func TestGatewayChargePrecisionRejectsRealDiscrepancies(t *testing.T) {
	for _, provider := range []string{"paypal", "stripe", "waffo_pancake", "clink"} {
		t.Run(provider, func(t *testing.T) {
			for _, quote := range []float64{10.066003086419753, 90.06690972222222, 10.064, 1.005} {
				charge := PaymentChargeAmount(provider, quote)
				require.True(t, PaymentChargeMatches(provider, quote, charge))
				for _, wrong := range []float64{charge - .01, charge + .01, charge - .05, 0, -1, math.NaN(), math.Inf(1)} {
					require.False(t, PaymentChargeMatches(provider, quote, wrong))
				}
			}
			require.False(t, PaymentChargeMatches(provider, math.NaN(), 10))
			require.False(t, PaymentChargeMatches(provider, math.Inf(1), 10))
		})
	}
	require.Equal(t, 1.01, PaymentChargeAmount("waffo_pancake", 1.005))
	require.Equal(t, 1.01, PaymentChargeAmount("clink", 1.005))
	require.False(t, PaymentChargeMatches("creem", 10.066, 10.07))
}

func TestWaffoLegacyUpgradeReferenceVerifiesExactChargedCents(t *testing.T) {
	oldDB, oldLog := model.DB, model.LOG_DB
	t.Cleanup(func() { model.DB = oldDB; model.LOG_DB = oldLog })
	db := setupWaffoPancakeTestDB(t)
	order := model.SubscriptionOrder{TradeNo: "waffo-fractional", Money: 90.06690972222222, PaymentProvider: "waffo_pancake", Status: "pending"}
	require.NoError(t, db.Create(&order).Error)
	payment := waffoRefundPayment{ID: "payment", OrderID: "order", OrderMerchantExternalID: order.TradeNo}
	payment.Snapshot.Subtotal = "90.07"
	trade, err := resolveVerifiedWaffoTradeNo(context.Background(), payment, false)
	require.NoError(t, err)
	require.Equal(t, order.TradeNo, trade)
	for _, amount := range []string{"90.06", "90.08", "NaN", "+Inf"} {
		payment.Snapshot.Subtotal = amount
		_, err = resolveVerifiedWaffoTradeNo(context.Background(), payment, false)
		require.Error(t, err)
	}
}
