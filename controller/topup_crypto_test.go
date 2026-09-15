package controller

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/require"
)

func TestCryptoAddressAndTransactionValidation(t *testing.T) {
	require.True(t, isValidCryptoAddress("tron", "TMYi3oRAS9hsJPq5sysKHgNsoRxm2CuuyU"))
	require.False(t, isValidCryptoAddress("tron", "TMYi3oRAS9hsJPq5sysKHgNsoRxm2CuuyV"))
	require.True(t, isValidCryptoAddress("solana", "CcVcaTMUtRBhTUkvNmDTt8tnCG3TU8unBwZTz4Pyp9UN"))
	require.True(t, isValidTxHash("tron", "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"))
	require.False(t, isValidTxHash("tron", "0x1234"))
	require.True(t, isValidTxHash("solana", encodeBase58(make([]byte, 64))))
}

func TestVerifySolanaWalletSignature(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	intent := model.CryptoDepositIntent{
		Chain:             "solana",
		WalletAddressFrom: encodeBase58(publicKey),
		Challenge:         "APIMaster deposit challenge",
	}
	signature := ed25519.Sign(privateKey, []byte(intent.Challenge))
	require.NoError(t, verifyWalletSignature(&intent, base64.StdEncoding.EncodeToString(signature)))
	require.Error(t, verifyWalletSignature(&intent, base64.StdEncoding.EncodeToString(make([]byte, 64))))
}

func TestVerifyTronIntent(t *testing.T) {
	from := "TMYi3oRAS9hsJPq5sysKHgNsoRxm2CuuyU"
	to := "TXLAQ63Xg1NAzckPwKHvzw7CSEmLMEqcdj"
	fromBytes, err := tronAddressBytes(from)
	require.NoError(t, err)
	toBytes, err := tronAddressBytes(to)
	require.NoError(t, err)

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if request.URL.Path == "/walletsolidity/gettransactioninfobyid" {
			fmt.Fprint(writer, `{"blockNumber":123,"receipt":{"net_fee":268000}}`)
			return
		}
		fmt.Fprintf(writer, `{"ret":[{"contractRet":"SUCCESS"}],"raw_data":{"contract":[{"type":"TransferContract","parameter":{"value":{"amount":2500000,"owner_address":"%x","to_address":"%x"}}}]}}`, fromBytes, toBytes)
	}))
	defer server.Close()

	txHash := strings.Repeat("a", 64)
	intent := model.CryptoDepositIntent{
		Chain: "tron", TokenSymbol: "TRX", WalletAddressFrom: from,
		ExpectedToAddress: to, TxHash: &txHash, AssetUsdPrice: 0.2,
	}
	usd, err := verifyTronIntent(&intent, cryptoChains["tron"], []string{server.URL})
	require.NoError(t, err)
	require.InDelta(t, 0.5, usd, 0.000001)
}

func TestVerifyTronUSDTIntent(t *testing.T) {
	from := "TMYi3oRAS9hsJPq5sysKHgNsoRxm2CuuyU"
	to := "TXLAQ63Xg1NAzckPwKHvzw7CSEmLMEqcdj"
	fromBytes, _ := tronAddressBytes(from)
	toBytes, _ := tronAddressBytes(to)
	tokenBytes, _ := tronAddressBytes(cryptoChains["tron"].usdtAddress)
	data := fmt.Sprintf("a9059cbb%064s%064x", fmt.Sprintf("%x", toBytes[1:]), 12_500_000)

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if request.URL.Path == "/walletsolidity/gettransactioninfobyid" {
			fmt.Fprint(writer, `{"blockNumber":123,"receipt":{"result":"SUCCESS"}}`)
			return
		}
		fmt.Fprintf(writer, `{"ret":[{"contractRet":"SUCCESS"}],"raw_data":{"contract":[{"type":"TriggerSmartContract","parameter":{"value":{"owner_address":"%x","contract_address":"%x","data":"%s"}}}]}}`, fromBytes, tokenBytes, data)
	}))
	defer server.Close()

	txHash := strings.Repeat("b", 64)
	intent := model.CryptoDepositIntent{
		Chain: "tron", TokenSymbol: "USDT", TokenAddress: cryptoChains["tron"].usdtAddress,
		WalletAddressFrom: from, ExpectedToAddress: to, TxHash: &txHash,
	}
	usd, err := verifyTronIntent(&intent, cryptoChains["tron"], []string{server.URL})
	require.NoError(t, err)
	require.InDelta(t, 12.5, usd, 0.000001)
}

func TestVerifySolanaIntents(t *testing.T) {
	from := "11111111111111111111111111111111"
	to := "CcVcaTMUtRBhTUkvNmDTt8tnCG3TU8unBwZTz4Pyp9UN"
	txHash := encodeBase58(append([]byte{1}, make([]byte, 63)...))

	t.Run("SOL", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(writer, `{"jsonrpc":"2.0","id":1,"result":{"meta":{"err":null},"transaction":{"message":{"accountKeys":[{"pubkey":"%s"}],"instructions":[{"parsed":{"type":"transfer","info":{"source":"%s","destination":"%s","lamports":1500000000}}}]}}}}`, from, from, to)
		}))
		defer server.Close()
		intent := model.CryptoDepositIntent{Chain: "solana", TokenSymbol: "SOL", WalletAddressFrom: from, ExpectedToAddress: to, TxHash: &txHash, AssetUsdPrice: 2}
		usd, err := verifySolanaIntent(&intent, cryptoChains["solana"], []string{server.URL})
		require.NoError(t, err)
		require.InDelta(t, 3, usd, 0.000001)
	})

	t.Run("USDT-SPL", func(t *testing.T) {
		mint := cryptoChains["solana"].usdtAddress
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(writer, `{"jsonrpc":"2.0","id":1,"result":{"meta":{"err":null,"preTokenBalances":[{"accountIndex":1,"mint":"%s","owner":"%s","uiTokenAmount":{"amount":"10000000"}}],"postTokenBalances":[{"accountIndex":1,"mint":"%s","owner":"%s","uiTokenAmount":{"amount":"5000000"}},{"accountIndex":2,"mint":"%s","owner":"%s","uiTokenAmount":{"amount":"5000000"}}]},"transaction":{"message":{"accountKeys":[{"pubkey":"%s"}],"instructions":[]}}}}`, mint, from, mint, from, mint, to, from)
		}))
		defer server.Close()
		intent := model.CryptoDepositIntent{Chain: "solana", TokenSymbol: "USDT", TokenAddress: mint, WalletAddressFrom: from, ExpectedToAddress: to, TxHash: &txHash}
		usd, err := verifySolanaIntent(&intent, cryptoChains["solana"], []string{server.URL})
		require.NoError(t, err)
		require.InDelta(t, 5, usd, 0.000001)
	})
}

func TestMatchCryptoAmountDiscountTier(t *testing.T) {
	originalDiscounts := operation_setting.GetPaymentSetting().AmountDiscount
	t.Cleanup(func() {
		operation_setting.GetPaymentSetting().AmountDiscount = originalDiscounts
	})

	testCases := []struct {
		name             string
		discounts        map[int]float64
		paid             float64
		expectedTier     int
		expectedDiscount float64
		applied          bool
	}{
		{
			name:             "no discount config keeps credited amount unchanged",
			discounts:        map[int]float64{},
			paid:             45,
			expectedTier:     0,
			expectedDiscount: 0,
			applied:          false,
		},
		{
			name:             "hitting configured fifty tier matches that tier",
			discounts:        map[int]float64{50: 0.9},
			paid:             45,
			expectedTier:     50,
			expectedDiscount: 0.9,
			applied:          true,
		},
		{
			name:             "larger crypto payments still use the matched tier discount factor",
			discounts:        map[int]float64{50: 0.9},
			paid:             100,
			expectedTier:     50,
			expectedDiscount: 0.9,
			applied:          true,
		},
		{
			name:             "payment below threshold does not trigger inflation",
			discounts:        map[int]float64{50: 0.9},
			paid:             44.99,
			expectedTier:     0,
			expectedDiscount: 0,
			applied:          false,
		},
		{
			name:             "invalid discounts are ignored",
			discounts:        map[int]float64{50: 1, 100: 0, 200: -0.5},
			paid:             100,
			expectedTier:     0,
			expectedDiscount: 0,
			applied:          false,
		},
		{
			name:             "highest eligible tier wins when multiple tiers are configured",
			discounts:        map[int]float64{50: 0.9, 100: 0.95},
			paid:             100,
			expectedTier:     100,
			expectedDiscount: 0.95,
			applied:          true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			operation_setting.GetPaymentSetting().AmountDiscount = tc.discounts
			actualTier, actualDiscount, applied := matchCryptoAmountDiscountTier(tc.paid)
			require.Equal(t, tc.applied, applied)
			require.Equal(t, tc.expectedTier, actualTier)
			require.InDelta(t, tc.expectedDiscount, actualDiscount, 0.000001)
		})
	}
}

func TestCryptoFirstTopupPromoMinPaidUSD(t *testing.T) {
	originalAmount := common.FirstTopupPromoAmount
	originalDiscount := common.FirstTopupPromoDiscount
	t.Cleanup(func() {
		common.FirstTopupPromoAmount = originalAmount
		common.FirstTopupPromoDiscount = originalDiscount
	})

	common.FirstTopupPromoAmount = 10
	common.FirstTopupPromoDiscount = 0.85
	require.InDelta(t, 8.5, cryptoFirstTopupPromoMinPaidUSD(), 0.000001)
}

func TestApplyCryptoFirstTopupPromoRequiresMinPaidThreshold(t *testing.T) {
	originalEnabled := common.FirstTopupPromoEnabled
	originalAmount := common.FirstTopupPromoAmount
	originalDiscount := common.FirstTopupPromoDiscount
	t.Cleanup(func() {
		common.FirstTopupPromoEnabled = originalEnabled
		common.FirstTopupPromoAmount = originalAmount
		common.FirstTopupPromoDiscount = originalDiscount
	})

	common.FirstTopupPromoEnabled = true
	common.FirstTopupPromoAmount = 10
	common.FirstTopupPromoDiscount = 0.85

	testCases := []struct {
		name           string
		paid           float64
		expectedCredit float64
		expectedBonus  float64
		applied        bool
	}{
		{
			name:           "eight point four nine does not trigger promo",
			paid:           8.49,
			expectedCredit: 8.49,
			expectedBonus:  0,
			applied:        false,
		},
		{
			name:           "eight point five credits ten",
			paid:           8.5,
			expectedCredit: 10,
			expectedBonus:  1.5,
			applied:        true,
		},
		{
			name:           "bonus remains capped by configured promo amount",
			paid:           20,
			expectedCredit: 20 + 10*(1/0.85-1),
			expectedBonus:  10 * (1/0.85 - 1),
			applied:        true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actualCredit, actualBonus, applied := applyCryptoFirstTopupPromo(tc.paid)
			require.Equal(t, tc.applied, applied)
			require.InDelta(t, tc.expectedCredit, actualCredit, 0.000001)
			require.InDelta(t, tc.expectedBonus, actualBonus, 0.000001)
		})
	}
}
