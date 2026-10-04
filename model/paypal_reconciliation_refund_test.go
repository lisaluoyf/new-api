package model

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPayPalRefundRecoveryRejectsMissingDuplicateAndWrongQuotaAudit(t *testing.T) {
	setupPaymentReconciliationTest(t)
	oldLogDB := LOG_DB
	LOG_DB = DB
	t.Cleanup(func() { LOG_DB = oldLogDB })
	require.NoError(t, LOG_DB.AutoMigrate(&Log{}))
	top := &TopUp{TradeNo: "anonymous-refund", UserId: 1, PaymentProvider: PaymentProviderPayPal, Status: "refunded", Money: 100, Amount: 100}
	valid := "PayPal 退款回收额度：退款金额 $100.00（订单 anonymous-refund，refund 已在 PayPal 完成），回收额度 ＄100.000000"
	ok, err := VerifyPayPalRefundRecovery(top)
	require.NoError(t, err)
	require.False(t, ok)
	log := Log{UserId: 1, Type: LogTypeRefund, Content: valid}
	require.NoError(t, LOG_DB.Create(&log).Error)
	ok, err = VerifyPayPalRefundRecovery(top)
	require.NoError(t, err)
	require.True(t, ok)
	require.NoError(t, LOG_DB.Model(&log).Update("content", "PayPal 退款回收额度：退款金额 $100.00（订单 anonymous-refund，refund 已在 PayPal 完成），回收额度 ＄99.000000").Error)
	ok, err = VerifyPayPalRefundRecovery(top)
	require.NoError(t, err)
	require.False(t, ok)
	require.NoError(t, LOG_DB.Model(&log).Update("content", valid).Error)
	require.NoError(t, LOG_DB.Create(&Log{UserId: 1, Type: LogTypeRefund, Content: valid}).Error)
	ok, err = VerifyPayPalRefundRecovery(top)
	require.NoError(t, err)
	require.False(t, ok)
	top.RefundedAmount = 100
	top.RefundedQuota = 50000000
	ok, err = VerifyPayPalRefundRecovery(top)
	require.NoError(t, err)
	require.True(t, ok)
	top.RefundedQuota--
	ok, err = VerifyPayPalRefundRecovery(top)
	require.NoError(t, err)
	require.False(t, ok)
}

func TestPayPalReversalPersistsQuotaEvidenceWithBalanceAndRemainsIdempotent(t *testing.T) {
	setupPaymentReconciliationTest(t)
	oldLogDB := LOG_DB
	LOG_DB = DB
	t.Cleanup(func() { LOG_DB = oldLogDB })
	require.NoError(t, LOG_DB.AutoMigrate(&Log{}))
	require.NoError(t, DB.Model(&User{}).Where("id = ?", 1).Update("quota", 50000100).Error)
	top := TopUp{TradeNo: "atomic-refund", UserId: 1, PaymentProvider: PaymentProviderPayPal, Status: "success", Money: 100, Amount: 100}
	require.NoError(t, DB.Create(&top).Error)
	quota, _, err := RefundPayPalTopUp(top.TradeNo, 100, "")
	require.NoError(t, err)
	require.Equal(t, 50000000, quota)
	require.NoError(t, DB.First(&top, top.Id).Error)
	require.Equal(t, float64(100), top.RefundedAmount)
	require.Equal(t, quota, top.RefundedQuota)
	ok, err := VerifyPayPalRefundRecovery(&top)
	require.NoError(t, err)
	require.True(t, ok)
	_, _, err = RefundPayPalTopUp(top.TradeNo, 100, "")
	require.ErrorIs(t, err, ErrTopUpStatusInvalid)
	var user User
	require.NoError(t, DB.First(&user, 1).Error)
	require.Equal(t, 100, user.Quota)
}

func TestPayPalSubscriptionRefundRequiresExactCancelledPurchaseCycle(t *testing.T) {
	cases := []struct {
		name string
		edit func(*SubscriptionOrder, *UserSubscription)
		want bool
	}{
		{"full purchase", func(*SubscriptionOrder, *UserSubscription) {}, true},
		{"partial refund", func(o *SubscriptionOrder, _ *UserSubscription) { o.RefundAmount = 20 }, false},
		{"wrong user", func(o *SubscriptionOrder, _ *UserSubscription) { o.UserId = 2 }, false},
		{"wrong amount", func(o *SubscriptionOrder, _ *UserSubscription) { o.Money = 70 }, false},
		{"wrong provider", func(o *SubscriptionOrder, _ *UserSubscription) { o.PaymentProvider = "clink" }, false},
		{"not refunded", func(o *SubscriptionOrder, _ *UserSubscription) { o.Status = "success" }, false},
		{"chargeback", func(o *SubscriptionOrder, _ *UserSubscription) { o.ChargebackAmount = 1 }, false},
		{"renewal", func(o *SubscriptionOrder, _ *UserSubscription) { o.OrderType = "renewal" }, false},
		{"upgrade", func(o *SubscriptionOrder, _ *UserSubscription) { o.OrderType = "upgrade" }, false},
		{"missing completion", func(o *SubscriptionOrder, _ *UserSubscription) { o.CompleteTime = 0 }, false},
		{"active entitlement", func(_ *SubscriptionOrder, s *UserSubscription) { s.Status = "active" }, false},
		{"expired entitlement", func(_ *SubscriptionOrder, s *UserSubscription) { s.Status = "expired" }, false},
		{"wrong cycle", func(_ *SubscriptionOrder, s *UserSubscription) { s.CurrentCycleId = 999 }, false},
		{"wrong plan", func(_ *SubscriptionOrder, s *UserSubscription) { s.PlanId = 2 }, false},
		{"wrong cycle owner", func(_ *SubscriptionOrder, s *UserSubscription) { s.UserId = 2 }, false},
		{"manual entitlement", func(_ *SubscriptionOrder, s *UserSubscription) { s.Source = "admin" }, false},
		{"future entitlement end", func(_ *SubscriptionOrder, s *UserSubscription) { s.EndTime = GetDBTimestamp() + 3600 }, false},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			setupGPTSubscriptionTestDB(t)
			now := GetDBTimestamp()
			o := SubscriptionOrder{TradeNo: "purchase", UserId: 1, PlanId: 1, Money: 69, RefundAmount: 69, Status: "refunded", PaymentProvider: "paypal", OrderType: "purchase", CompleteTime: now - 100}
			require.NoError(t, DB.Create(&o).Error)
			s := UserSubscription{UserId: 1, PlanId: 1, CurrentCycleId: o.Id, Status: "cancelled", Source: "order", StartTime: now - 100, EndTime: now - 1}
			tt.edit(&o, &s)
			require.NoError(t, DB.Save(&o).Error)
			require.NoError(t, DB.Create(&s).Error)
			beforeOrder, beforeCycle := o, s
			ok, err := VerifyPayPalSubscriptionRefundRecovery("purchase", 1, 69)
			require.NoError(t, err)
			require.Equal(t, tt.want, ok)
			require.NoError(t, DB.First(&o, o.Id).Error)
			require.NoError(t, DB.First(&s, s.Id).Error)
			require.Equal(t, beforeOrder, o)
			require.Equal(t, beforeCycle, s)
		})
	}
	t.Run("missing and duplicate cycle", func(t *testing.T) {
		setupGPTSubscriptionTestDB(t)
		now := GetDBTimestamp()
		o := SubscriptionOrder{TradeNo: "purchase", UserId: 1, PlanId: 1, Money: 69, RefundAmount: 69, Status: "refunded", PaymentProvider: "paypal", OrderType: "purchase", CompleteTime: now - 100}
		require.NoError(t, DB.Create(&o).Error)
		ok, err := VerifyPayPalSubscriptionRefundRecovery(o.TradeNo, 1, 69)
		require.NoError(t, err)
		require.False(t, ok)
		for n := 0; n < 2; n++ {
			require.NoError(t, DB.Create(&UserSubscription{UserId: 1, PlanId: 1, CurrentCycleId: o.Id, Status: "cancelled", Source: "order", StartTime: now - 100, EndTime: now - 1}).Error)
		}
		ok, err = VerifyPayPalSubscriptionRefundRecovery(o.TradeNo, 1, 69)
		require.NoError(t, err)
		require.False(t, ok)
	})
}
