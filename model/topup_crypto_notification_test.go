package model

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestFormatNowPaymentsAssetLabel(t *testing.T) {
	tests := []struct {
		payCurrency string
		network     string
		want        string
	}{
		{payCurrency: "usdtbsc", network: "bsc", want: "USDT BSC"},
		{payCurrency: "usdttrc20", network: "trx", want: "USDT TRON"},
		{payCurrency: "usdcsol", network: "sol", want: "USDC SOLANA"},
		{payCurrency: "btc", network: "btc", want: "BTC BITCOIN"},
	}

	for _, test := range tests {
		require.Equal(t, test.want, formatNowPaymentsAssetLabel(test.payCurrency, test.network))
	}
	require.Equal(t, "加密货币（NOWPayments）", FormatPaymentMethodLabel(PaymentMethodNowPayments))
}

func TestCryptoDepositAssetLabel(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&CryptoDepositIntent{}))
	DB = db

	txHash := "0x1234"
	intent := CryptoDepositIntent{
		Id:          "intent-1",
		Chain:       "bsc",
		TokenSymbol: "USDT",
		TxHash:      &txHash,
		Status:      "completed",
	}
	require.NoError(t, db.Create(&intent).Error)
	require.Equal(t, "USDT BSC", cryptoDepositAssetLabel("CRYPTO:BSC:"+txHash))
}
