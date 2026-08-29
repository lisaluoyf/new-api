package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestAdminTopupPaymentMethodFilterMatchesListAndExport(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&User{}, &TopUp{}))
	DB = db

	require.NoError(t, db.Create(&[]TopUp{
		{UserId: 1, TradeNo: "paypal-success", PaymentMethod: PaymentMethodPayPal, Status: "success", CreateTime: 3},
		{UserId: 1, TradeNo: "stripe-success", PaymentMethod: PaymentMethodStripe, Status: "success", CreateTime: 2},
		{UserId: 1, TradeNo: "paypal-pending", PaymentMethod: PaymentMethodPayPal, Status: "pending", CreateTime: 1},
	}).Error)

	pageInfo := &common.PageInfo{Page: 1, PageSize: 10}
	listed, total, err := GetAllTopUps("success", PaymentMethodPayPal, "", pageInfo)
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Equal(t, "paypal-success", listed[0].TradeNo)

	exported, err := ExportAllTopUps("", "success", PaymentMethodPayPal, "")
	require.NoError(t, err)
	require.Len(t, exported, 1)
	require.Equal(t, listed[0].TradeNo, exported[0].TradeNo)
}

func TestAdminTopupTransactionTypeFilterAndEnrichment(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&User{}, &TopUp{}, &SubscriptionPlan{}, &SubscriptionOrder{}))
	DB = db

	plan := SubscriptionPlan{Title: "Pro+", PlanType: SubscriptionPlanTypeGPTSubscription}
	require.NoError(t, db.Create(&plan).Error)
	require.NoError(t, db.Create(&[]TopUp{
		{UserId: 1, TradeNo: "wallet-order", Status: common.TopUpStatusSuccess, CreateTime: 2},
		{UserId: 1, TradeNo: "subscription-order", Status: common.TopUpStatusSuccess, CreateTime: 1},
	}).Error)
	require.NoError(t, db.Create(&SubscriptionOrder{
		UserId:          1,
		PlanId:          plan.Id,
		TradeNo:         "subscription-order",
		OrderType:       "upgrade",
		PaymentMethod:   "wxpay",
		PaymentProvider: PaymentProviderEpay,
		ProviderPayload: common.GetJsonString(map[string]any{
			"payment_snapshot": map[string]any{
				"charge_amount":   "70.00",
				"charge_currency": "CNY",
			},
		}),
		Status: common.TopUpStatusSuccess,
	}).Error)

	pageInfo := &common.PageInfo{Page: 1, PageSize: 10}
	subscriptionRows, total, err := GetAllTopUps("", "", TopupTransactionTypeSubscription, pageInfo)
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Equal(t, "subscription-order", subscriptionRows[0].TradeNo)

	walletRows, total, err := GetAllTopUps("", "", TopupTransactionTypeWallet, pageInfo)
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Equal(t, "wallet-order", walletRows[0].TradeNo)

	allRows, err := ExportAllTopUps("", "", "", "")
	require.NoError(t, err)
	EnrichTopupsWithTransactionInfo(allRows)
	byTradeNo := make(map[string]*TopUp, len(allRows))
	for _, row := range allRows {
		byTradeNo[row.TradeNo] = row
	}
	require.Equal(t, TopupTransactionTypeWallet, byTradeNo["wallet-order"].TransactionType)
	require.Equal(t, TopupTransactionTypeSubscription, byTradeNo["subscription-order"].TransactionType)
	require.Equal(t, "Pro+", byTradeNo["subscription-order"].SubscriptionPlanTitle)
	require.Equal(t, "upgrade", byTradeNo["subscription-order"].SubscriptionOrderType)
	require.Equal(t, "¥70.00", byTradeNo["subscription-order"].ActualPayment)
}

func TestSubscriptionOrderInsertCreatesVisiblePendingTransaction(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&User{}, &TopUp{}, &SubscriptionPlan{}, &SubscriptionOrder{}))
	DB = db

	plan := SubscriptionPlan{Title: "Coding Starter", PlanType: SubscriptionPlanTypeCodingPlan}
	require.NoError(t, db.Create(&plan).Error)
	order := SubscriptionOrder{
		UserId: 6811, PlanId: plan.Id, Money: 4.99,
		TradeNo: "pending-coding-renewal", OrderType: "renewal",
		PaymentMethod: PaymentMethodPlatega, PaymentProvider: PaymentProviderPlatega,
		Status: common.TopUpStatusPending, CreateTime: 12345,
	}
	require.NoError(t, order.Insert())

	topup := GetTopUpByTradeNo(order.TradeNo)
	require.NotNil(t, topup)
	require.Equal(t, common.TopUpStatusPending, topup.Status)
	require.Equal(t, order.Money, topup.CreditedAmount)
	require.Zero(t, topup.CompleteTime)

	rows, total, err := GetAllTopUps("", "", TopupTransactionTypeSubscription, &common.PageInfo{Page: 1, PageSize: 10})
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	EnrichTopupsWithTransactionInfo(rows)
	require.Equal(t, "Coding Starter", rows[0].SubscriptionPlanTitle)
	require.Equal(t, "renewal", rows[0].SubscriptionOrderType)
}

func TestBackfillSubscriptionTopUpHistoryRestoresMissingOrders(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&TopUp{}, &SubscriptionOrder{}))
	DB = db

	missing := SubscriptionOrder{
		UserId: 6811, PlanId: 8, Money: 4.989284,
		TradeNo: "SUB-PLATEGA-6811-missing", OrderType: "purchase",
		PaymentMethod: PaymentMethodPlatega, PaymentProvider: PaymentProviderPlatega,
		Status: common.TopUpStatusPending, CreateTime: 1787936521,
	}
	require.NoError(t, db.Create(&missing).Error)
	require.Nil(t, GetTopUpByTradeNo(missing.TradeNo))

	require.NoError(t, BackfillSubscriptionTopUpHistory())
	require.NoError(t, BackfillSubscriptionTopUpHistory())
	topup := GetTopUpByTradeNo(missing.TradeNo)
	require.NotNil(t, topup)
	require.Equal(t, common.TopUpStatusPending, topup.Status)
	var count int64
	require.NoError(t, db.Model(&TopUp{}).Where("trade_no = ?", missing.TradeNo).Count(&count).Error)
	require.EqualValues(t, 1, count)
}
