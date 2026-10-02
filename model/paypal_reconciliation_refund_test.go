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
