package controller

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func TestCryptoUpgradeFreezesQuoteBeforeSigning(t *testing.T) {
	db := setupCryptoPersistenceTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.SubscriptionPlan{}, &model.SubscriptionOrder{}, &model.UserSubscription{}))
	user := model.User{Id: 90267, Username: "crypto-upgrade-precision", Password: "password"}
	require.NoError(t, db.Create(&user).Error)
	starter := model.SubscriptionPlan{Id: 90264, Title: "Starter", PlanType: model.SubscriptionPlanTypeGPTSubscription,
		PriceAmount: 10, TierLevel: 2, Enabled: true, DurationUnit: "day", DurationValue: 30}
	pro := starter
	pro.Id, pro.Title, pro.PriceAmount, pro.TierLevel = 90265, "Pro", 20, 3
	require.NoError(t, db.Create(&starter).Error)
	require.NoError(t, db.Create(&pro).Error)
	now := model.GetDBTimestamp()
	previous := model.UserSubscription{UserId: user.Id, PlanId: starter.Id, Status: "active",
		StartTime: now - 117505, EndTime: now + 2474495, DurationSecondsSnapshot: 2592000,
		PriceAmountSnapshot: 10, TierLevelSnapshot: 2}
	require.NoError(t, db.Create(&previous).Error)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Set("id", user.Id)
	context.Request = httptest.NewRequest(http.MethodPost, "/api/user/crypto/intent", bytes.NewBufferString(
		fmt.Sprintf(`{"chain":"bsc","token_symbol":"USDT","wallet_address_from":"0x8ba42f5bee2bb9fe99e644acca608769fb2e50fa","plan_id":%d}`, pro.Id)))
	context.Request.Header.Set("Content-Type", "application/json")
	CreateCryptoDepositIntent(context)
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	var response struct {
		BaseUnits string  `json:"baseUnitAmount"`
		Amount    float64 `json:"expectedUsdAmount"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	var intent model.CryptoDepositIntent
	require.NoError(t, db.First(&intent, "user_id = ?", user.Id).Error)
	var order model.SubscriptionOrder
	require.NoError(t, db.First(&order, "trade_no = ?", intent.SubscriptionOrderTradeNo).Error)
	require.Equal(t, "upgrade", order.OrderType)
	require.Equal(t, previous.Id, order.PreviousSubscriptionId)
	amount := decimal.NewFromFloat(intent.ExpectedUsdAmount)
	require.True(t, amount.Equal(amount.Round(6)), "quote must survive SQL decimal(18,6)")
	require.Equal(t, intent.ExpectedUsdAmount, intent.CreditUsdAmount)
	require.Equal(t, intent.ExpectedUsdAmount, order.Money)
	require.Equal(t, intent.ExpectedUsdAmount, response.Amount)
	units := amount.Shift(18).BigInt().String()
	require.Equal(t, units, intent.ExpectedBaseUnits)
	require.Equal(t, units, response.BaseUnits)
	require.Contains(t, intent.Challenge, "Exact Base Units: "+units)
	require.NoError(t, verifyExpectedBaseUnits(&intent, mustBigInt(units)))
	short := mustBigInt(units)
	short.Sub(short, mustBigInt("1"))
	require.Error(t, verifyExpectedBaseUnits(&intent, short), "one base unit short must still fail")
}

func TestCryptoExpiryPreservesTransactionFailureAndDoesNotOverwriteSettlement(t *testing.T) {
	db := setupCryptoPersistenceTestDB(t)
	hash := "0x" + strings.Repeat("f", 64)
	intent := model.CryptoDepositIntent{Id: "precision-timeout", UserId: 901,
		Chain: "bsc", TokenSymbol: "USDT", TxHash: &hash, Status: "pending",
		Purpose: cryptoIntentPurposeGPTSubscription, ErrorMessage: "transfer amount mismatch"}
	require.NoError(t, db.Create(&intent).Error)
	stale := intent
	markCryptoIntentExpired(&intent)
	var persisted model.CryptoDepositIntent
	require.NoError(t, db.First(&persisted, "id = ?", intent.Id).Error)
	require.Equal(t, "expired", persisted.Status)
	require.Contains(t, persisted.ErrorMessage, "contact support")
	require.Contains(t, persisted.ErrorMessage, "transfer amount mismatch")
	require.Equal(t, &hash, persisted.TxHash)
	markCryptoIntentExpired(&stale)
	var count int64
	require.NoError(t, db.Model(&model.Log{}).Where("user_id = ?", intent.UserId).Count(&count).Error)
	require.EqualValues(t, 1, count, "timeout is audited once")
	settled := model.CryptoDepositIntent{Id: "settled-race", UserId: 902,
		Chain: "bsc", Status: "pending", TxHash: &hash}
	// Different chain avoids the chain/hash unique constraint used by the intent.
	settled.Chain = "eth"
	require.NoError(t, db.Create(&settled).Error)
	require.NoError(t, db.Model(&settled).Update("status", "confirmed").Error)
	settled.Status = "pending" // a stale expiry scan must not replace confirmed.
	markCryptoIntentExpired(&settled)
	persisted = model.CryptoDepositIntent{}
	require.NoError(t, db.First(&persisted, "id = ?", settled.Id).Error)
	require.Equal(t, "confirmed", persisted.Status)
}

func TestCryptoRetryPersistsLatestVerificationError(t *testing.T) {
	db := setupCryptoPersistenceTestDB(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":null}`)
	}))
	defer server.Close()
	old := cryptoChains["bsc"]
	cfg := old
	cfg.defaultRPCs = []string{server.URL}
	cryptoChains["bsc"] = cfg
	t.Setenv("CRYPTO_RPC_BSC", server.URL)
	t.Cleanup(func() { cryptoChains["bsc"] = old })
	hash := "0x" + strings.Repeat("b", 64)
	intent := model.CryptoDepositIntent{Id: "retry-evidence", UserId: 903, Chain: "bsc",
		Status: "pending", TxHash: &hash}
	require.NoError(t, db.Create(&intent).Error)
	verifyAndCredit(intent.Id)
	require.NoError(t, db.First(&intent, "id = ?", intent.Id).Error)
	require.Equal(t, "pending", intent.Status)
	require.Equal(t, "transaction receipt is not available yet", intent.ErrorMessage)
	require.Positive(t, intent.NextCheckAt)
	require.Equal(t, 1, intent.RetryCount)
}
