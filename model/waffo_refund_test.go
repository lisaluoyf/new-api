package model

import (
	"math"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

func setupWaffoRefundTest(t *testing.T) WaffoRefund {
	t.Helper()
	setupAtomicBillingTestDB(t)
	oldRedis := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = oldRedis })
	require.NoError(t, DB.AutoMigrate(&TopUp{}, &WaffoRefund{}, &SubscriptionOrder{}))
	sqlDB, err := DB.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, DB.Create(&User{Id: 1, Username: "waffo-refund", Quota: 10 * int(common.QuotaPerUnit)}).Error)
	require.NoError(t, DB.Create(&TopUp{UserId: 1, TradeNo: "trade", Amount: 10, Money: 7.5, PaymentProvider: PaymentProviderWaffoPancake, PaymentMethod: PaymentMethodWaffoPancake, Status: common.TopUpStatusSuccess, WaffoPaymentID: "PAY_1", WaffoOrderID: "ORD_1"}).Error)
	return WaffoRefund{TicketID: "TKT_1", PaymentID: "PAY_1", OrderID: "ORD_1", StoreID: "STO_1", TradeNo: "trade", Currency: "USD", Amount: 3, PaymentAmount: 7.5, Status: "processing", Version: 1, ProviderUpdatedAt: 1000}
}

func assertWaffoBalances(t *testing.T, available, frozen, refunded float64) {
	t.Helper()
	var user User
	var order TopUp
	require.NoError(t, DB.First(&user, 1).Error)
	require.NoError(t, DB.Where("trade_no = ?", "trade").First(&order).Error)
	require.Equal(t, int(available*common.QuotaPerUnit), user.Quota)
	require.Equal(t, int(frozen*common.QuotaPerUnit), order.RefundFrozenQuota)
	require.Equal(t, int(refunded*common.QuotaPerUnit), order.RefundedQuota)
}

func TestWaffoRefundFreezeSuccessAndDuplicate(t *testing.T) {
	entry := setupWaffoRefundTest(t)
	require.NoError(t, ApplyWaffoRefund(entry))
	assertWaffoBalances(t, 6, 4, 0)
	require.NoError(t, ApplyWaffoRefund(entry))
	assertWaffoBalances(t, 6, 4, 0)
	entry.Status = "succeeded"
	entry.ProviderUpdatedAt++
	require.NoError(t, ApplyWaffoRefund(entry))
	assertWaffoBalances(t, 6, 0, 4)
	entry.Status = "failed"
	entry.ProviderUpdatedAt++
	require.NoError(t, ApplyWaffoRefund(entry))
	assertWaffoBalances(t, 6, 0, 4)
}

func TestWaffoRefundFailureStaleAndResubmission(t *testing.T) {
	entry := setupWaffoRefundTest(t)
	require.NoError(t, ApplyWaffoRefund(entry))
	stale := entry
	entry.Status = "failed"
	entry.ProviderUpdatedAt++
	require.NoError(t, ApplyWaffoRefund(entry))
	assertWaffoBalances(t, 10, 0, 0)
	require.NoError(t, ApplyWaffoRefund(stale))
	assertWaffoBalances(t, 10, 0, 0)
	entry.Version++
	entry.ProviderUpdatedAt++
	entry.Status = "under_review"
	require.NoError(t, ApplyWaffoRefund(entry))
	assertWaffoBalances(t, 6, 4, 0)
}

func TestWaffoRefundInsufficientBalanceAndSpending(t *testing.T) {
	entry := setupWaffoRefundTest(t)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", 1).Update("quota", int(common.QuotaPerUnit)).Error)
	require.NoError(t, ApplyWaffoRefund(entry))
	assertWaffoBalances(t, -3, 4, 0)
	require.Error(t, AdjustWalletAndTokenQuota(1, -1, 0, "", 0, true))
	entry.Status = "rejected"
	entry.ProviderUpdatedAt++
	require.NoError(t, ApplyWaffoRefund(entry))
	assertWaffoBalances(t, 1, 0, 0)
}

func TestWaffoRefundCumulativePartialAndFull(t *testing.T) {
	entry := setupWaffoRefundTest(t)
	entry.Status = "succeeded"
	require.NoError(t, ApplyWaffoRefund(entry))
	entry.TicketID = "TKT_2"
	entry.Amount = 4.5
	entry.Status = "processing"
	require.NoError(t, ApplyWaffoRefund(entry))
	assertWaffoBalances(t, 0, 6, 4)
	entry.Status = "succeeded"
	entry.ProviderUpdatedAt++
	require.NoError(t, ApplyWaffoRefund(entry))
	assertWaffoBalances(t, 0, 0, 10)
	var order TopUp
	require.NoError(t, DB.Where("trade_no = ?", "trade").First(&order).Error)
	require.Equal(t, common.TopUpStatusRefunded, order.Status)
	entry.TicketID = "TKT_3"
	entry.Amount = 0.01
	require.Error(t, ApplyWaffoRefund(entry))
	assertWaffoBalances(t, 0, 0, 10)
}

func TestWaffoRefundActualAmountReduced(t *testing.T) {
	entry := setupWaffoRefundTest(t)
	require.NoError(t, ApplyWaffoRefund(entry))
	entry.Status = "succeeded"
	entry.Amount = 1.5
	entry.ProviderUpdatedAt++
	require.NoError(t, ApplyWaffoRefund(entry))
	assertWaffoBalances(t, 8, 0, 2)
}

func TestWaffoRefundRejectsInvalidAndSubscription(t *testing.T) {
	entry := setupWaffoRefundTest(t)
	for _, modify := range []func(*WaffoRefund){
		func(e *WaffoRefund) { e.Amount = math.NaN() },
		func(e *WaffoRefund) { e.Amount = math.Inf(1) },
		func(e *WaffoRefund) { e.Currency = "EUR" },
		func(e *WaffoRefund) { e.PaymentID = "PAY_wrong" },
		func(e *WaffoRefund) { e.Amount = 8 },
		func(e *WaffoRefund) { e.Status = "unknown" },
	} {
		bad := entry
		modify(&bad)
		require.Error(t, ApplyWaffoRefund(bad))
		assertWaffoBalances(t, 10, 0, 0)
	}
	require.NoError(t, DB.Create(&SubscriptionOrder{TradeNo: "trade", UserId: 1}).Error)
	require.ErrorContains(t, ApplyWaffoRefund(entry), "entitlement review")
	assertWaffoBalances(t, 10, 0, 0)
}

func TestWaffoRefundConcurrentRetries(t *testing.T) {
	entry := setupWaffoRefundTest(t)
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- ApplyWaffoRefund(entry) }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	assertWaffoBalances(t, 6, 4, 0)
	var count int64
	require.NoError(t, DB.Model(&WaffoRefund{}).Count(&count).Error)
	require.EqualValues(t, 1, count)
}

func TestWaffoPaymentBindingCannotBeReassigned(t *testing.T) {
	setupWaffoRefundTest(t)
	require.NoError(t, BindWaffoPayment("trade", "PAY_1", "ORD_1"))
	require.Error(t, BindWaffoPayment("trade", "PAY_other", "ORD_1"))
}
