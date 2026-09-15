package model

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCryptoDepositIntentPreservesNonEVMAddressCase(t *testing.T) {
	for _, chain := range []string{"tron", "solana"} {
		intent := CryptoDepositIntent{
			Chain:             chain,
			TokenAddress:      "AbCdEf123",
			WalletAddressFrom: "WalletAbCdEf",
			ExpectedToAddress: "RecipientAbCdEf",
		}
		require.NoError(t, intent.BeforeCreate(nil))
		require.Equal(t, "AbCdEf123", intent.TokenAddress)
		require.Equal(t, "WalletAbCdEf", intent.WalletAddressFrom)
		require.Equal(t, "RecipientAbCdEf", intent.ExpectedToAddress)
	}
}

func TestCryptoDepositIntentNormalizesEVMAddressCase(t *testing.T) {
	intent := CryptoDepositIntent{
		Chain:             "BSC",
		WalletAddressFrom: "0xABCDEF",
		ExpectedToAddress: "0xFEDCBA",
	}
	require.NoError(t, intent.BeforeCreate(nil))
	require.Equal(t, "bsc", intent.Chain)
	require.Equal(t, "0xabcdef", intent.WalletAddressFrom)
	require.Equal(t, "0xfedcba", intent.ExpectedToAddress)
}
