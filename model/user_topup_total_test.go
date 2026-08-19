package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestEnrichUsersTotalTopupUSDUsesFrozenUSDValues(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&User{}, &TopUp{}))
	DB = db

	require.NoError(t, db.Create(&[]TopUp{
		// CNY and RUB Money values must not be mixed into the USD total.
		{UserId: 1, TradeNo: "cny", Amount: 10, Money: 52.5, PaymentMethod: "alipay", Status: common.TopUpStatusSuccess},
		{UserId: 1, TradeNo: "rub", Amount: 5, Money: 417.75, PaymentMethod: PaymentMethodPlatega, Status: common.TopUpStatusSuccess},
		// Precise credited USD wins over the legacy integer Amount.
		{UserId: 1, TradeNo: "precise", Amount: 7, CreditedAmount: 7.25, Money: 7.25, Status: common.TopUpStatusSuccess},
		// Non-successful orders do not count.
		{UserId: 1, TradeNo: "pending", Amount: 100, Money: 100, Status: common.TopUpStatusPending},
		{UserId: 1, TradeNo: "refunded", Amount: 200, Money: 200, Status: "refunded"},
		{UserId: 2, TradeNo: "other-user", Amount: 3, Money: 3, Status: common.TopUpStatusSuccess},
	}).Error)

	users := []*User{{Id: 1}, {Id: 2}, {Id: 3}}
	require.NoError(t, EnrichUsersTotalTopupUSD(users))
	require.InDelta(t, 22.25, users[0].TotalTopupUSD, 0.000001)
	require.InDelta(t, 3, users[1].TotalTopupUSD, 0.000001)
	require.Zero(t, users[2].TotalTopupUSD)
}

func TestGetUserPaidAmountUSDUsesActualPaymentSnapshot(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&TopUp{}))
	DB = db
	require.NoError(t, db.Create(&[]TopUp{
		{UserId: 7, TradeNo: "promo", PaidAmountUSD: 7.5, CreditedAmount: 10, Money: 7.5, PaymentProvider: PaymentProviderStripe, Status: common.TopUpStatusSuccess},
		{UserId: 7, TradeNo: "pending", PaidAmountUSD: 100, Status: common.TopUpStatusPending},
	}).Error)
	total, err := GetUserPaidAmountUSD(7)
	require.NoError(t, err)
	require.InDelta(t, 7.5, total, 0.000001)
}
