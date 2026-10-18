package controller

import (
	"context"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
)

func TestReconciliationCryptoAmountPrecision(t *testing.T) {
	cfg := cryptoChains["bsc"]
	tests := []struct {
		name, actual string
		edit         func(*reconciliationCandidate, *model.CryptoDepositIntent)
		wantError    bool
	}{
		{name: "original exact units", actual: "10453314043209877000"},
		{name: "six decimal settlement", actual: "10453314000000000000"},
		{name: "within same rounded micro dollar", actual: "10453314499999999999"},
		{name: "below rounding boundary", actual: "10453313499999999999", wantError: true},
		{name: "above rounding boundary", actual: "10453314500000000000", wantError: true},
		{name: "one micro dollar short", actual: "10453313000000000000", wantError: true},
		{name: "pending intent", actual: "10453314000000000000", edit: func(_ *reconciliationCandidate, i *model.CryptoDepositIntent) { i.Status = "pending" }, wantError: true},
		{name: "unpaid order", actual: "10453314000000000000", edit: func(r *reconciliationCandidate, _ *model.CryptoDepositIntent) { r.status = "pending" }, wantError: true},
		{name: "different local amount", actual: "10453314000000000000", edit: func(r *reconciliationCandidate, _ *model.CryptoDepositIntent) { r.money = 11 }, wantError: true},
		{name: "different currency", actual: "10453314000000000000", edit: func(r *reconciliationCandidate, _ *model.CryptoDepositIntent) { r.currency = "CNY" }, wantError: true},
		{name: "native asset", actual: "10453314000000000000", edit: func(_ *reconciliationCandidate, i *model.CryptoDepositIntent) { i.TokenAddress = "" }, wantError: true},
		{name: "unknown token", actual: "10453314000000000000", edit: func(_ *reconciliationCandidate, i *model.CryptoDepositIntent) { i.TokenSymbol = "OTHER" }, wantError: true},
		{name: "wrong token address", actual: "10453314000000000000", edit: func(_ *reconciliationCandidate, i *model.CryptoDepositIntent) {
			i.TokenAddress = "0x" + strings.Repeat("3", 40)
		}, wantError: true},
		{name: "invalid expectation", actual: "10453314000000000000", edit: func(_ *reconciliationCandidate, i *model.CryptoDepositIntent) { i.ExpectedBaseUnits = "invalid" }, wantError: true},
		{name: "zero transfer", actual: "0", wantError: true},
		{name: "nonfinite local amount", actual: "10453314000000000000", edit: func(r *reconciliationCandidate, _ *model.CryptoDepositIntent) { r.money = math.NaN() }, wantError: true},
		{name: "USDC uses its configured precision", actual: "10453314000000000000", edit: func(_ *reconciliationCandidate, i *model.CryptoDepositIntent) {
			i.TokenSymbol, i.TokenAddress = "USDC", cfg.usdcAddress
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := reconciliationCandidate{status: "success", currency: "USD", money: 10.453314}
			i := model.CryptoDepositIntent{Status: model.CryptoDepositIntentStatusConfirmed, TokenSymbol: "USDT", TokenAddress: cfg.usdtAddress, ExpectedBaseUnits: "10453314043209877000"}
			if tc.edit != nil {
				tc.edit(&r, &i)
			}
			before := i
			err := reconciliationCryptoAmountCheck(r, cfg)(&i, mustBigInt(tc.actual))
			if tc.wantError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, before, i, "auditing must not mutate the payment intent")
			if tc.actual != i.ExpectedBaseUnits {
				require.Error(t, verifyExpectedBaseUnits(&i, mustBigInt(tc.actual)), "payment settlement must remain strict")
			}
		})
	}
	// A six-decimal chain must still reject even one missing base unit.
	i := &model.CryptoDepositIntent{Status: "confirmed", TokenSymbol: "USDT", TokenAddress: cryptoChains["arbitrum"].usdtAddress, ExpectedBaseUnits: "10453314"}
	r := reconciliationCandidate{status: "success", currency: "USD", money: 10.453314}
	require.Error(t, reconciliationCryptoAmountCheck(r, cryptoChains["arbitrum"])(i, mustBigInt("10453313")))
}

func TestReconciliationCryptoVerifiesReceiptAndPreservesPayment(t *testing.T) {
	for _, fault := range []string{"", "wrong sender", "wrong recipient", "wrong token", "failed transaction", "insufficient confirmations", "short payment"} {
		t.Run("receipt/"+fault, func(t *testing.T) {
			db := setupCryptoPersistenceTestDB(t)
			cfg := cryptoChains["bsc"]
			wallet, recipient := "0x"+strings.Repeat("1", 40), "0x"+strings.Repeat("2", 40)
			logWallet, logRecipient, logToken := wallet, recipient, cfg.usdtAddress
			status, confirmations := "0x1", int64(cryptoEVMConfirmations)
			amount := mustBigInt("10453314000000000000")
			switch fault {
			case "wrong sender":
				logWallet = "0x" + strings.Repeat("3", 40)
			case "wrong recipient":
				logRecipient = "0x" + strings.Repeat("3", 40)
			case "wrong token":
				logToken = "0x" + strings.Repeat("3", 40)
			case "failed transaction":
				status = "0x0"
			case "insufficient confirmations":
				confirmations--
			case "short payment":
				amount = mustBigInt("10453313000000000000")
			}
			receipt := map[string]interface{}{"status": status, "blockNumber": "0x100", "logs": []interface{}{map[string]interface{}{
				"address": logToken, "topics": []interface{}{transferEventTopic, "0x" + strings.Repeat("0", 24) + logWallet[2:], "0x" + strings.Repeat("0", 24) + logRecipient[2:]}, "data": fmt.Sprintf("0x%064x", amount),
			}}}
			rpc := newCryptoEVMTestRPC(t, receipt, map[string]interface{}{"from": wallet, "to": cfg.usdtAddress}, 0x100+confirmations)
			t.Setenv("CRYPTO_RPC_BSC", rpc.URL)
			hash := "0x" + strings.Repeat("a", 64)
			intent := model.CryptoDepositIntent{Id: "precision-audit", Chain: "bsc", TokenSymbol: "USDT", TokenAddress: strings.ToLower(cfg.usdtAddress), WalletAddressFrom: wallet, ExpectedToAddress: recipient, ExpectedBaseUnits: "10453314043209877000", TxHash: &hash, Status: model.CryptoDepositIntentStatusConfirmed, Purpose: "gpt_subscription", SubscriptionOrderTradeNo: "precision-order", ExpectedUsdAmount: 10.453314043209877, CreditUsdAmount: 10.453314, ConfirmedAt: 123}
			require.NoError(t, db.Create(&intent).Error)
			before := intent
			r := reconciliationCandidate{trade: "precision-order", provider: "crypto", purpose: "subscription", status: "success", currency: "USD", money: 10.453314}
			proof := queryReconciliationOrder(context.Background(), r)
			item := classifyReconciliationOrder(r, proof)
			if fault == "" {
				require.Equal(t, "matched", item.Result)
				require.Equal(t, "10.453314", item.OfficialAmount)
				require.Empty(t, item.Problem)
			} else {
				require.Equal(t, "unverified", item.Result)
				require.Equal(t, "chain_verification_failed", item.Problem)
			}
			var after model.CryptoDepositIntent
			require.NoError(t, db.First(&after, "id = ?", intent.Id).Error)
			require.Equal(t, before, after)
			_, err := verifyIntentOnChain(&intent, cfg, []string{rpc.URL})
			require.Error(t, err, "settlement must reject the same rounded payment")
		})
	}
}
