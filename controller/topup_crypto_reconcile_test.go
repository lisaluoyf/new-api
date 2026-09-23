package controller

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupCryptoPersistenceTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	previousDB := model.DB
	previousLogDB := model.LOG_DB
	previousRedisEnabled := common.RedisEnabled
	model.DB = db
	model.LOG_DB = db
	common.RedisEnabled = false
	t.Cleanup(func() {
		model.DB = previousDB
		model.LOG_DB = previousLogDB
		common.RedisEnabled = previousRedisEnabled
	})
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.TopUp{}, &model.CryptoDepositIntent{}))
	require.NoError(t, db.AutoMigrate(&model.Log{}))
	return db
}

func TestVerifyExpectedCryptoBaseUnits(t *testing.T) {
	intent := &model.CryptoDepositIntent{ExpectedBaseUnits: "1000000"}
	require.NoError(t, verifyExpectedBaseUnits(intent, mustBigInt("1000000")))
	err := verifyExpectedBaseUnits(intent, mustBigInt("999999"))
	var retryable *cryptoRetryableError
	require.ErrorAs(t, err, &retryable)
}

func TestCryptoRetryDelaysDifferentiatePendingConfirmationFromRPCFailure(t *testing.T) {
	var pending *cryptoRetryableError
	require.ErrorAs(t, pendingCryptoRetryableError("transaction confirmations are not sufficient"), &pending)
	require.Equal(t, cryptoVerificationPendingRetrySeconds, pending.retryAfterSeconds)

	var unavailable *cryptoRetryableError
	require.ErrorAs(t, retryableCryptoError("latest block unavailable"), &unavailable)
	require.Equal(t, cryptoVerificationUnavailableRetrySeconds, unavailable.retryAfterSeconds)
}

func TestFindUniqueCryptoRecoveryCandidateRejectsAmbiguity(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	previousDB := model.DB
	model.DB = db
	t.Cleanup(func() { model.DB = previousDB })
	require.NoError(t, db.AutoMigrate(&model.CryptoDepositIntent{}))

	now := int64(1_000)
	base := model.CryptoDepositIntent{
		UserId: 1, Chain: "bsc", TokenSymbol: "USDT", TokenAddress: "0xtoken",
		WalletAddressFrom: "0xfrom", ExpectedToAddress: "0xto", ExpectedBaseUnits: "100",
		WalletSignature: "signature", AuthorizedAt: now, Status: model.CryptoDepositIntentStatusPending,
		CreatedAt: now - 10, RecoveryExpiresAt: now + 100,
		TopUpTradeNo: "crypto-1",
	}
	require.NoError(t, db.Create(&base).Error)
	second := base
	second.Id = "second"
	second.UserId = 2
	second.TopUpTradeNo = "crypto-2"
	require.NoError(t, db.Create(&second).Error)

	_, unique, err := findUniqueCryptoRecoveryCandidate(cryptoIncomingTransfer{
		Chain: "bsc", TokenSymbol: "USDT", TokenAddress: "0xtoken", From: "0xfrom", To: "0xto",
		AmountBaseUnits: "100", BlockTime: now,
	})
	require.NoError(t, err)
	require.False(t, unique)
}

func TestBindRecoveredCryptoHashIsIdempotent(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	previousDB := model.DB
	model.DB = db
	t.Cleanup(func() { model.DB = previousDB })
	require.NoError(t, db.AutoMigrate(&model.CryptoDepositIntent{}))

	intent := model.CryptoDepositIntent{
		Id: "intent-1", UserId: 1, Chain: "bsc", Status: model.CryptoDepositIntentStatusPending,
		WalletSignature: "signature", RecoveryExpiresAt: 2_000,
	}
	require.NoError(t, db.Create(&intent).Error)
	bound, err := bindRecoveredCryptoHash(intent.Id, "bsc", "0xhash", 1_000)
	require.NoError(t, err)
	require.True(t, bound)
	bound, err = bindRecoveredCryptoHash(intent.Id, "bsc", "0xhash", 1_000)
	require.NoError(t, err)
	require.False(t, bound)

	var restored model.CryptoDepositIntent
	require.NoError(t, db.First(&restored, "id = ?", intent.Id).Error)
	require.NotNil(t, restored.TxHash)
	require.Equal(t, "0xhash", *restored.TxHash)
	require.False(t, errors.Is(err, gorm.ErrDuplicatedKey))
}

func TestCreateCryptoDepositIntentCreatesPendingTopup(t *testing.T) {
	db := setupCryptoPersistenceTestDB(t)
	require.NoError(t, db.Create(&model.User{Id: 101, Username: "crypto-create", Password: "password"}).Error)
	previousMinTopup := operation_setting.MinTopUp
	operation_setting.MinTopUp = 1
	t.Cleanup(func() { operation_setting.MinTopUp = previousMinTopup })

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Set("id", 101)
	context.Request = httptest.NewRequest(http.MethodPost, "/api/user/crypto/intent", bytes.NewBufferString(`{"chain":"bsc","token_symbol":"USDT","wallet_address_from":"0x8ba42f5bee2bb9fe99e644acca608769fb2e50fa","expected_usd_amount":25}`))
	context.Request.Header.Set("Content-Type", "application/json")

	CreateCryptoDepositIntent(context)
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())

	var intent model.CryptoDepositIntent
	require.NoError(t, db.First(&intent, "user_id = ?", 101).Error)
	require.Equal(t, "25000000000000000000", intent.ExpectedBaseUnits)
	require.NotEmpty(t, intent.TopUpTradeNo)

	var topUp model.TopUp
	require.NoError(t, db.First(&topUp, "trade_no = ?", intent.TopUpTradeNo).Error)
	require.Equal(t, common.TopUpStatusPending, topUp.Status)
	require.Equal(t, model.PaymentProviderCrypto, topUp.PaymentProvider)
}

func TestCreateCryptoDepositIntentLocksDiscountCreditAmount(t *testing.T) {
	db := setupCryptoPersistenceTestDB(t)
	require.NoError(t, db.Create(&model.User{Id: 106, Username: "crypto-promo", Password: "password"}).Error)
	previousMinTopup := operation_setting.MinTopUp
	previousDiscounts := operation_setting.GetPaymentSetting().AmountDiscount
	previousExpiries := operation_setting.GetPaymentSetting().AmountDiscountExpiresAt
	operation_setting.MinTopUp = 1
	operation_setting.GetPaymentSetting().AmountDiscount = map[int]float64{50: 0.95, 100: 0.9}
	operation_setting.GetPaymentSetting().AmountDiscountExpiresAt = map[int]int64{}
	t.Cleanup(func() {
		operation_setting.MinTopUp = previousMinTopup
		operation_setting.GetPaymentSetting().AmountDiscount = previousDiscounts
		operation_setting.GetPaymentSetting().AmountDiscountExpiresAt = previousExpiries
	})

	testCases := []struct {
		name    string
		paid    float64
		credit  float64
		wantErr bool
	}{
		{name: "fifty dollar tier", paid: 47.5, credit: 50},
		{name: "hundred dollar tier", paid: 90, credit: 100},
		{name: "custom amount unchanged", paid: 55, credit: 55},
		{name: "reject forged credit", paid: 55, credit: 100, wantErr: true},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			recorder := httptest.NewRecorder()
			context, _ := gin.CreateTestContext(recorder)
			context.Set("id", 106)
			body := fmt.Sprintf(`{"chain":"bsc","token_symbol":"USDT","wallet_address_from":"0x8ba42f5bee2bb9fe99e644acca608769fb2e50fa","expected_usd_amount":%.2f,"credit_usd_amount":%.2f}`, tc.paid, tc.credit)
			context.Request = httptest.NewRequest(http.MethodPost, "/api/user/crypto/intent", strings.NewReader(body))
			context.Request.Header.Set("Content-Type", "application/json")
			CreateCryptoDepositIntent(context)
			if tc.wantErr {
				require.Equal(t, http.StatusBadRequest, recorder.Code, recorder.Body.String())
				return
			}
			require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
			var intent model.CryptoDepositIntent
			require.NoError(t, db.Order("rowid DESC").First(&intent).Error)
			require.InDelta(t, tc.paid, intent.ExpectedUsdAmount, 0.000001)
			require.InDelta(t, tc.credit, intent.CreditUsdAmount, 0.000001)
			var topUp model.TopUp
			require.NoError(t, db.First(&topUp, "trade_no = ?", intent.TopUpTradeNo).Error)
			require.InDelta(t, tc.credit, topUp.CreditedAmount, 0.000001)
			require.InDelta(t, tc.paid, topUp.Money, 0.000001)
		})
	}
}

func TestAuthorizeCryptoDepositIntentPersistsSignature(t *testing.T) {
	db := setupCryptoPersistenceTestDB(t)
	require.NoError(t, db.Create(&model.User{Id: 102, Username: "crypto-authorize", Password: "password"}).Error)
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	challenge := "APIMaster test authorization"
	intent := model.CryptoDepositIntent{
		Id: "authorize-intent", UserId: 102, Chain: "solana", TokenSymbol: "SOL",
		WalletAddressFrom: encodeBase58(publicKey), ExpectedToAddress: "CcVcaTMUtRBhTUkvNmDTt8tnCG3TU8unBwZTz4Pyp9UN",
		Challenge: challenge, Purpose: cryptoIntentPurposeWalletTopup, Status: model.CryptoDepositIntentStatusPending,
		ExpiresAt: common.GetTimestamp() + 300, RecoveryExpiresAt: common.GetTimestamp() + 3600,
	}
	require.NoError(t, db.Create(&intent).Error)
	signature := base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, []byte(challenge)))

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Set("id", 102)
	context.Params = gin.Params{{Key: "id", Value: intent.Id}}
	context.Request = httptest.NewRequest(http.MethodPost, "/api/user/crypto/intent/authorize-intent/authorize", bytes.NewBufferString(`{"wallet_signature":"`+signature+`"}`))
	context.Request.Header.Set("Content-Type", "application/json")

	AuthorizeCryptoDepositIntent(context)
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.NoError(t, db.First(&intent, "id = ?", intent.Id).Error)
	require.Equal(t, signature, intent.WalletSignature)
	require.Positive(t, intent.AuthorizedAt)
}

func TestSettleCryptoWalletTopupCreditsExistingOrderOnlyOnce(t *testing.T) {
	db := setupCryptoPersistenceTestDB(t)
	user := model.User{Id: 103, Username: "crypto-settle", Password: "password", Quota: 10}
	require.NoError(t, db.Create(&user).Error)
	txHash := "0x" + strings.Repeat("a", 64)
	intent := model.CryptoDepositIntent{
		Id: "settle-intent", UserId: user.Id, Chain: "bsc", TokenSymbol: "USDT",
		WalletAddressFrom: "0x8ba42f5bee2bb9fe99e644acca608769fb2e50fa",
		ExpectedToAddress: "0x33de43dad6955655ec0543f32069ac331e633c9c",
		Challenge:         "challenge", Purpose: cryptoIntentPurposeWalletTopup,
		TopUpTradeNo: "CRYPTO:settle-intent", Status: model.CryptoDepositIntentStatusPending,
		TxHash: &txHash, ExpiresAt: common.GetTimestamp() + 300, RecoveryExpiresAt: common.GetTimestamp() + 3600,
	}
	require.NoError(t, db.Create(&intent).Error)
	require.NoError(t, db.Create(&model.TopUp{
		UserId: user.Id, TradeNo: intent.TopUpTradeNo, PaymentMethod: model.PaymentMethodCrypto,
		PaymentProvider: model.PaymentProviderCrypto, Status: common.TopUpStatusPending,
	}).Error)

	credited, err := settleCryptoWalletTopup(&intent, 25, 25, 250_000, intent.TopUpTradeNo, common.GetTimestamp())
	require.NoError(t, err)
	require.True(t, credited)
	credited, err = settleCryptoWalletTopup(&intent, 25, 25, 250_000, intent.TopUpTradeNo, common.GetTimestamp())
	require.NoError(t, err)
	require.False(t, credited)

	require.NoError(t, db.First(&user, user.Id).Error)
	require.Equal(t, 250_010, user.Quota)
	var topUp model.TopUp
	require.NoError(t, db.First(&topUp, "trade_no = ?", intent.TopUpTradeNo).Error)
	require.Equal(t, common.TopUpStatusSuccess, topUp.Status)
	require.InDelta(t, 25, topUp.CreditedAmount, 0.000001)
	require.NoError(t, db.First(&intent, "id = ?", intent.Id).Error)
	require.Equal(t, model.CryptoDepositIntentStatusConfirmed, intent.Status)
}

func TestSettleCryptoWalletTopupSeparatesPaidAndCreditedAmounts(t *testing.T) {
	db := setupCryptoPersistenceTestDB(t)
	user := model.User{Id: 107, Username: "crypto-credit", Password: "password", Quota: 0}
	require.NoError(t, db.Create(&user).Error)
	txHash := "0x" + strings.Repeat("c", 64)
	intent := model.CryptoDepositIntent{
		Id: "settle-credit-intent", UserId: user.Id, Chain: "bsc", TokenSymbol: "USDT",
		WalletAddressFrom: "0x8ba42f5bee2bb9fe99e644acca608769fb2e50fa",
		ExpectedToAddress: "0x33de43dad6955655ec0543f32069ac331e633c9c",
		Challenge:         "challenge", Purpose: cryptoIntentPurposeWalletTopup, TopUpTradeNo: "CRYPTO:settle-credit",
		ExpectedUsdAmount: 47.5, CreditUsdAmount: 50, Status: model.CryptoDepositIntentStatusPending,
		TxHash: &txHash, ExpiresAt: common.GetTimestamp() + 300, RecoveryExpiresAt: common.GetTimestamp() + 3600,
	}
	require.NoError(t, db.Create(&intent).Error)
	require.NoError(t, db.Create(&model.TopUp{
		UserId: user.Id, TradeNo: intent.TopUpTradeNo, PaymentMethod: model.PaymentMethodCrypto,
		PaymentProvider: model.PaymentProviderCrypto, Status: common.TopUpStatusPending,
	}).Error)

	credited, err := settleCryptoWalletTopup(&intent, 47.5, intent.CreditUsdAmount, 500_000, intent.TopUpTradeNo, common.GetTimestamp())
	require.NoError(t, err)
	require.True(t, credited)
	var topUp model.TopUp
	require.NoError(t, db.First(&topUp, "trade_no = ?", intent.TopUpTradeNo).Error)
	require.InDelta(t, 50, topUp.CreditedAmount, 0.000001)
	require.InDelta(t, 47.5, topUp.PaidAmountUSD, 0.000001)
	require.InDelta(t, 47.5, topUp.Money, 0.000001)
	require.Equal(t, int64(50), topUp.Amount)
	require.NoError(t, db.First(&user, user.Id).Error)
	require.Equal(t, 500_000, user.Quota)
}

func TestMarkCryptoIntentFailedUpdatesOrderAtomically(t *testing.T) {
	db := setupCryptoPersistenceTestDB(t)
	intent := model.CryptoDepositIntent{
		Id: "failed-intent", UserId: 104, Chain: "bsc", TokenSymbol: "USDT",
		WalletAddressFrom: "0x8ba42f5bee2bb9fe99e644acca608769fb2e50fa",
		ExpectedToAddress: "0x33de43dad6955655ec0543f32069ac331e633c9c",
		Challenge:         "challenge", Purpose: cryptoIntentPurposeWalletTopup,
		TopUpTradeNo: "CRYPTO:failed-intent", Status: model.CryptoDepositIntentStatusPending,
		ExpiresAt: common.GetTimestamp() + 300, RecoveryExpiresAt: common.GetTimestamp() + 3600,
	}
	require.NoError(t, db.Create(&intent).Error)
	require.NoError(t, db.Create(&model.TopUp{
		UserId: intent.UserId, TradeNo: intent.TopUpTradeNo, PaymentMethod: model.PaymentMethodCrypto,
		PaymentProvider: model.PaymentProviderCrypto, Status: common.TopUpStatusPending,
	}).Error)

	markCryptoIntentFailed(intent.Id, errors.New("invalid recipient"))
	require.NoError(t, db.First(&intent, "id = ?", intent.Id).Error)
	require.Equal(t, model.CryptoDepositIntentStatusFailed, intent.Status)
	var topUp model.TopUp
	require.NoError(t, db.First(&topUp, "trade_no = ?", intent.TopUpTradeNo).Error)
	require.Equal(t, common.TopUpStatusFailed, topUp.Status)
}

func TestCancelCryptoDepositIntentRejectsClaimedTransaction(t *testing.T) {
	db := setupCryptoPersistenceTestDB(t)
	require.NoError(t, db.Create(&model.User{Id: 105, Username: "crypto-cancel", Password: "password"}).Error)
	txHash := "0x" + strings.Repeat("b", 64)
	intent := model.CryptoDepositIntent{
		Id: "cancel-intent", UserId: 105, Chain: "bsc", TokenSymbol: "USDT",
		WalletAddressFrom: "0x8ba42f5bee2bb9fe99e644acca608769fb2e50fa",
		ExpectedToAddress: "0x33de43dad6955655ec0543f32069ac331e633c9c",
		Challenge:         "challenge", Purpose: cryptoIntentPurposeWalletTopup,
		TopUpTradeNo: "CRYPTO:cancel-intent", Status: model.CryptoDepositIntentStatusPending,
		TxHash: &txHash, ExpiresAt: common.GetTimestamp() + 300, RecoveryExpiresAt: common.GetTimestamp() + 3600,
	}
	require.NoError(t, db.Create(&intent).Error)
	require.NoError(t, db.Create(&model.TopUp{
		UserId: intent.UserId, TradeNo: intent.TopUpTradeNo, PaymentMethod: model.PaymentMethodCrypto,
		PaymentProvider: model.PaymentProviderCrypto, Status: common.TopUpStatusPending,
	}).Error)

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Set("id", intent.UserId)
	context.Params = gin.Params{{Key: "id", Value: intent.Id}}
	context.Request = httptest.NewRequest(http.MethodPost, "/api/user/crypto/intent/cancel-intent/cancel", nil)

	CancelCryptoDepositIntent(context)
	require.Equal(t, http.StatusConflict, recorder.Code, recorder.Body.String())
	require.NoError(t, db.First(&intent, "id = ?", intent.Id).Error)
	require.Equal(t, model.CryptoDepositIntentStatusPending, intent.Status)
	var topUp model.TopUp
	require.NoError(t, db.First(&topUp, "trade_no = ?", intent.TopUpTradeNo).Error)
	require.Equal(t, common.TopUpStatusPending, topUp.Status)
}

func TestSolanaTokenCursorKeyUsesAccountAddress(t *testing.T) {
	first := solanaTokenCursorKey("account-a")
	second := solanaTokenCursorKey("account-b")
	require.NotEqual(t, first, second)
	require.LessOrEqual(t, len(first), 32)
}

func TestCryptoDepositRecoveryWindows(t *testing.T) {
	require.Equal(t, 30*time.Minute, cryptoUnsignedIntentWindow)
	require.Equal(t, 10*time.Minute, cryptoSignedTransferWindow)
	require.Equal(t, 30*time.Minute, cryptoHashVerificationWindow)
}

func mustBigInt(value string) *big.Int {
	result, ok := new(big.Int).SetString(value, 10)
	if !ok {
		panic("invalid test integer")
	}
	return result
}
