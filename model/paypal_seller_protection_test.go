package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestPayPalProtectionPersistenceAndSubscriptionMirror(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	// Simulate an existing table before adding the protection columns.
	type legacyTopUp struct {
		Id      int
		TradeNo string `gorm:"unique;type:varchar(255);index"`
	}
	require.NoError(t, db.Table("top_ups").AutoMigrate(&legacyTopUp{}))
	require.NoError(t, db.Exec("INSERT INTO top_ups (id, trade_no) VALUES (1, 'legacy')").Error)
	require.NoError(t, db.AutoMigrate(&TopUp{}))
	var legacy TopUp
	require.NoError(t, db.First(&legacy, 1).Error)
	require.Empty(t, legacy.PayPalSellerProtection.Status)

	payload := `{"id":"CAPTURE-1","seller_protection":{"status":"PARTIALLY_ELIGIBLE","dispute_categories":["ITEM_NOT_RECEIVED"]}}`
	var capture PayPalCaptureMetadata
	require.NoError(t, common.UnmarshalJsonStr(payload, &capture))
	wallet := TopUp{TradeNo: "wallet", PaymentProvider: PaymentProviderPayPal}
	wallet.ApplyPayPalCapture(capture)
	require.NoError(t, db.Create(&wallet).Error)
	var restored TopUp
	require.NoError(t, db.First(&restored, wallet.Id).Error)
	require.Equal(t, capture.SellerProtection, restored.PayPalSellerProtection)
	require.Equal(t, "CAPTURE-1", restored.PayPalCaptureID)
	require.Contains(t, restored.PayPalProtectionNotificationLine(), "部分符合资格（未收到商品）")

	order := SubscriptionOrder{TradeNo: "subscription", PaymentProvider: PaymentProviderPayPal, PaymentMethod: PaymentMethodPayPal, Status: common.TopUpStatusPending}
	require.NoError(t, upsertSubscriptionTopUpTx(db, &order))
	order.Status = common.TopUpStatusSuccess
	order.ProviderPayload = payload
	require.NoError(t, upsertSubscriptionTopUpTx(db, &order))
	var mirror TopUp
	require.NoError(t, db.Where("trade_no = ?", order.TradeNo).First(&mirror).Error)
	require.Equal(t, capture.SellerProtection, mirror.PayPalSellerProtection)
	require.Equal(t, restored.PayPalProtectionNotificationLine(), mirror.PayPalProtectionNotificationLine())
	// A replay without metadata must not erase a known assessment.
	order.ProviderPayload = `{}`
	require.NoError(t, upsertSubscriptionTopUpTx(db, &order))
	require.NoError(t, db.Where("trade_no = ?", order.TradeNo).First(&mirror).Error)
	require.Equal(t, capture.SellerProtection, mirror.PayPalSellerProtection)
}

func TestPayPalProtectionUnknownAndProviderIsolation(t *testing.T) {
	for _, status := range []string{"", "UNRECOGNIZED", "NOT_ELIGIBLE", "ELIGIBLE", "PARTIALLY_ELIGIBLE"} {
		t.Run(status, func(t *testing.T) {
			capture := PayPalCaptureMetadata{ID: "CAPTURE", SellerProtection: PayPalSellerProtection{Status: status}}
			topup := TopUp{PaymentProvider: PaymentProviderPayPal}
			topup.ApplyPayPalCapture(capture)
			line := topup.PayPalProtectionNotificationLine()
			switch status {
			case "", "UNRECOGNIZED":
				require.Empty(t, topup.PayPalSellerProtection.Status)
				require.Contains(t, line, "未知")
			case "NOT_ELIGIBLE":
				require.Contains(t, line, "不符合资格")
			default:
				require.Contains(t, line, "符合资格")
			}
			other := TopUp{PaymentProvider: PaymentProviderStripe}
			other.ApplyPayPalCapture(capture)
			require.Empty(t, other.PayPalCaptureID)
			require.Empty(t, other.PayPalProtectionNotificationLine())
		})
	}
}

func TestPayPalRechargePersistsProtectionBeforeSuccessHook(t *testing.T) {
	t.Setenv("FEISHU_OPS_CHAT_ID", "")
	t.Setenv("GA_MP_API_SECRET", "")
	truncateTables(t)
	user := User{Username: "paypal-protection-wallet", Quota: 0}
	require.NoError(t, DB.Create(&user).Error)
	topup := TopUp{UserId: user.Id, TradeNo: "paypal-protection-wallet", Amount: 10, Money: 10, PaymentProvider: PaymentProviderPayPal, PaymentMethod: PaymentMethodPayPal, Status: common.TopUpStatusPending}
	require.NoError(t, DB.Create(&topup).Error)
	capture := PayPalCaptureMetadata{ID: "CAPTURE-WALLET", SellerProtection: PayPalSellerProtection{Status: "ELIGIBLE", DisputeCategories: []string{"UNAUTHORIZED_TRANSACTION"}}}
	require.NoError(t, RechargePayPal(topup.TradeNo, "", capture))
	restored := GetTopUpByTradeNo(topup.TradeNo)
	require.NotNil(t, restored)
	require.Equal(t, common.TopUpStatusSuccess, restored.Status)
	require.Equal(t, capture.SellerProtection, restored.PayPalSellerProtection)
	require.Contains(t, restored.PayPalProtectionNotificationLine(), "未经授权交易")
	require.Error(t, RechargePayPal(topup.TradeNo, "", capture))
	require.NoError(t, DB.First(&user, user.Id).Error)
	require.Equal(t, int(10*common.QuotaPerUnit), user.Quota, "duplicate webhook must not credit twice")
}
