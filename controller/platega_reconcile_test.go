package controller

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestPlategaCallbackRequiresOfficialVerificationAndRetainsRetry(t *testing.T) {
	db := setupCryptoPersistenceTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.PlategaOrder{}, &model.PlategaEvent{}, &model.PlategaObservation{}, &model.SubscriptionOrder{}))
	t.Setenv("PLATEGA_MERCHANT_ID", "test-merchant")
	t.Setenv("PLATEGA_X_SECRET", "test-secret")
	t.Setenv("FEISHU_OPS_CHAT_ID", "")
	t.Setenv("GA_MP_API_SECRET", "")
	oldEnabled, oldQuery := setting.PlategaEnabled, queryPlategaStatus
	setting.PlategaEnabled = true
	t.Cleanup(func() { setting.PlategaEnabled = oldEnabled; queryPlategaStatus = oldQuery })
	require.NoError(t, db.Create(&model.User{Id: 1, Username: "anonymous"}).Error)
	require.NoError(t, db.Create(&model.TopUp{UserId: 1, TradeNo: "test-order", Amount: 10, Money: 10, PaymentProvider: model.PaymentProviderPlatega, PaymentMethod: model.PaymentMethodPlatega, Status: "pending"}).Error)
	require.NoError(t, db.Create(&model.PlategaOrder{UserId: 1, TradeNo: "test-order", Payload: "test-order", PlategaTransactionId: "test-transaction", RubAmount: 100, CreateRequestJSON: `{"payload":"test-order","paymentDetails":{"amount":100,"currency":"RUB"}}`, CreateResponseJSON: `{"id":"test-transaction","merchantId":"test-merchant","paymentDetails":"108.50 RUB"}`}).Error)
	timeout := true
	calls := 0
	queryPlategaStatus = func(context.Context, string) (*service.PlategaTransactionStatusResponse, error) {
		calls++
		if timeout {
			return nil, errors.New("timeout")
		}
		return &service.PlategaTransactionStatusResponse{ID: "test-transaction", MerchantID: "test-merchant", Payload: "test-order", PaymentDetails: service.PlategaPaymentDetails{Amount: 108.5, Currency: "RUB"}, PaymentMethod: "SBPQR", Status: "CONFIRMED", RawJSON: `{"status":"CONFIRMED"}`}, nil
	}
	body := `{"id":"test-transaction","payload":"test-order","status":"CONFIRMED","amount":108.50,"currency":"RUB","paymentMethod":2}`
	invoke := func(payload, secret string) int {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("POST", "/api/payment/platega/callback", strings.NewReader(payload))
		c.Request.Header.Set("X-MerchantId", "test-merchant")
		if secret != "" {
			c.Request.Header.Set("X-Secret", secret)
		}
		PlategaCallback(c)
		return w.Code
	}
	require.Equal(t, 401, invoke(body, ""))
	require.Equal(t, 401, invoke(body, "wrong"))
	require.Zero(t, calls)
	for _, bad := range []string{strings.Replace(body, `"amount":108.50`, `"amount":0`, 1), strings.Replace(body, `"currency":"RUB"`, `"currency":"USD"`, 1), strings.Replace(body, `"id":"test-transaction"`, `"id":"test-transaction","transactionId":"different"`, 1)} {
		require.Equal(t, 400, invoke(bad, "test-secret"))
	}
	require.Zero(t, calls)
	var count int64
	require.NoError(t, db.Model(&model.PlategaEvent{}).Count(&count).Error)
	require.Zero(t, count)
	require.Equal(t, 503, invoke(body, "test-secret"))
	require.Equal(t, "pending", model.GetTopUpByTradeNo("test-order").Status)
	// The ordinary admin completion route must not bypass upstream verification.
	adminResponse := httptest.NewRecorder()
	adminContext, _ := gin.CreateTestContext(adminResponse)
	adminContext.Set("id", 10)
	adminContext.Request = httptest.NewRequest("POST", "/api/user/topup/complete", strings.NewReader(`{"trade_no":"test-order"}`))
	adminContext.Request.Header.Set("Content-Type", "application/json")
	AdminCompleteTopUp(adminContext)
	require.Equal(t, 503, adminResponse.Code)
	require.Equal(t, "pending", model.GetTopUpByTradeNo("test-order").Status)
	var e model.PlategaEvent
	require.NoError(t, db.First(&e).Error)
	require.Equal(t, "retry", e.Status)
	timeout = false
	require.Equal(t, 200, invoke(body, "test-secret"))
	require.Equal(t, "success", model.GetTopUpByTradeNo("test-order").Status)
	var user model.User
	require.NoError(t, db.First(&user, 1).Error)
	credited := user.Quota
	require.Greater(t, credited, 0)
	require.Equal(t, 200, invoke(body, "test-secret"))
	require.NoError(t, db.First(&user, 1).Error)
	require.Equal(t, credited, user.Quota)
}

func TestPlategaWholeRefundRetriesReviewAndReversesFrozenGrantOnce(t *testing.T) {
	db := setupCryptoPersistenceTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.PlategaOrder{}, &model.PlategaEvent{}, &model.PlategaObservation{}, &model.SubscriptionOrder{}))
	t.Setenv("PLATEGA_MERCHANT_ID", "test-merchant")
	t.Setenv("PLATEGA_X_SECRET", "test-secret")
	oldEnabled, oldQuery := setting.PlategaEnabled, queryPlategaStatus
	setting.PlategaEnabled = true
	t.Cleanup(func() { setting.PlategaEnabled = oldEnabled; queryPlategaStatus = oldQuery })
	// Includes a historical bonus. Neither the invoice nor today's Amount/
	// CreditedAmount can replace the original 7M grant. Preserve consumed debt.
	require.NoError(t, db.Create(&model.User{Id: 1, Username: "anonymous", Quota: 6983581, UsedQuota: 16419}).Error)
	require.NoError(t, db.Create(&model.TopUp{UserId: 1, TradeNo: "test-order", Amount: 10, CreditedAmount: 99, Money: 710.18, PaymentProvider: model.PaymentProviderPlatega, Status: "success"}).Error)
	require.NoError(t, db.Create(&model.PlategaOrder{UserId: 1, TradeNo: "test-order", Payload: "test-order", PlategaTransactionId: "test-transaction", RubAmount: 710.18, GrantedQuota: 7000000, PlategaStatus: "confirmed", CreateRequestJSON: `{"payload":"test-order","paymentDetails":{"amount":710.18,"currency":"RUB"}}`, CreateResponseJSON: `{"id":"test-transaction","merchantId":"test-merchant","paymentDetails":"770.55 RUB"}`}).Error)
	refundStatus := "PENDING"
	apiStatus := "CHARGEBACKED"
	queryPlategaStatus = func(context.Context, string) (*service.PlategaTransactionStatusResponse, error) {
		return &service.PlategaTransactionStatusResponse{ID: "test-transaction", MerchantID: "test-merchant", Payload: "test-order", PaymentDetails: service.PlategaPaymentDetails{Amount: 770.55, Currency: "RUB"}, PaymentMethod: "SBPQR", Status: apiStatus, RefundStatus: &refundStatus, RawJSON: `{"status":"CHARGEBACKED"}`}, nil
	}
	body := `{"id":"test-transaction","payload":"test-order","status":"CHARGEBACKED","amount":770.55,"currency":"RUB","paymentMethod":2}`
	invoke := func(payload string) int {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("POST", "/api/payment/platega/callback", strings.NewReader(payload))
		c.Request.Header.Set("X-MerchantId", "test-merchant")
		c.Request.Header.Set("X-Secret", "test-secret")
		PlategaCallback(c)
		return w.Code
	}
	assertWallet := func(quota int) {
		var u model.User
		require.NoError(t, db.First(&u, 1).Error)
		require.Equal(t, quota, u.Quota)
		require.Equal(t, 16419, u.UsedQuota)
	}
	// Unfinished refunds remain reviewable, but no longer display confirmed.
	for _, state := range []string{"", "PENDING", "FAILED"} {
		refundStatus = state
		require.Equal(t, 200, invoke(body))
		assertWallet(6983581)
		require.Equal(t, "chargeback", model.GetPlategaOrderByTradeNo("test-order").PlategaStatus)
	}
	refundStatus = "COMPLETED"
	apiStatus = "CONFIRMED"
	require.Equal(t, 200, invoke(body))
	assertWallet(6983581)
	apiStatus = "CHARGEBACKED"
	// An audit write failure must roll back the wallet and retain retry state.
	require.NoError(t, db.Callback().Create().Before("gorm:create").Register("fail-refund-audit", func(tx *gorm.DB) {
		if tx.Statement.Table == "platega_observations" {
			tx.AddError(errors.New("injected audit failure"))
		}
	}))
	require.Equal(t, 503, invoke(body))
	assertWallet(6983581)
	require.NoError(t, db.Callback().Create().Remove("fail-refund-audit"))
	// Retry the original review event after the official refund completes.
	var e model.PlategaEvent
	require.NoError(t, db.First(&e).Error)
	require.NoError(t, processPlategaEvent(context.Background(), &e))
	assertWallet(-16419)
	order := model.GetPlategaOrderByTradeNo("test-order")
	require.EqualValues(t, 7000000, order.ReversedQuota)
	require.Equal(t, 710.18, order.ReversedRub)
	require.Empty(t, order.ReviewReason)
	top := model.GetTopUpByTradeNo("test-order")
	require.Equal(t, "refunded", top.Status)
	require.EqualValues(t, 7000000, top.RefundedQuota)
	require.Equal(t, 710.18, top.RefundedAmount)
	// Repeated notification, plus a distinct canonical event, both stay harmless.
	require.Equal(t, 200, invoke(body))
	require.Equal(t, 200, invoke(strings.Replace(body, `"paymentMethod":2`, `"paymentMethod":"SBPQR"`, 1)))
	assertWallet(-16419)
	var observations []model.PlategaObservation
	require.NoError(t, db.Find(&observations).Error)
	var total int64
	for _, observation := range observations {
		total += observation.QuotaDelta
	}
	require.EqualValues(t, -7000000, total)
}
