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
