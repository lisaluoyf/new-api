package controller

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestClinkFailedAttemptThenPaidSession(t *testing.T) {
	db := setupCryptoPersistenceTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.SubscriptionOrder{}))
	require.NoError(t, db.Create(&model.User{Id: 12833, Username: "clink-recovery"}).Error)
	order := model.TopUp{UserId: 12833, TradeNo: "CLINK-test", Status: "pending", Amount: 1, Money: 1, PaymentProvider: "clink", PaymentMethod: "clink"}
	require.NoError(t, db.Create(&order).Error)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/", nil)
	failure := []byte(`{"type":"order.failed","data":{"object":{"merchantReferenceId":"CLINK-test","orderId":"failed-attempt"}}}`)
	require.NoError(t, handleClinkWebhook(c, failure))
	var current model.TopUp
	require.NoError(t, db.First(&current, order.Id).Error)
	require.Equal(t, "pending", current.Status)
	// Also exercise an order left failed by the old production handler.
	require.NoError(t, db.Model(&current).Update("status", "failed").Error)
	oldQuery, oldCurrency := getClinkSessionForConfirmation, setting.ClinkCurrency
	setting.ClinkCurrency = "USD"
	t.Cleanup(func() { getClinkSessionForConfirmation = oldQuery; setting.ClinkCurrency = oldCurrency })
	calls := 0
	getClinkSessionForConfirmation = func(ctx context.Context, id string) (*service.ClinkCheckoutSessionDetail, error) {
		calls++
		require.Equal(t, "sess_test", id)
		return &service.ClinkCheckoutSessionDetail{SessionID: id, PaymentStatus: "paid", MerchantReferenceID: "CLINK-test", OriginalCurrency: "USD", PaymentCurrency: "VND", AmountSubtotal: 1, AmountTotal: 26989}, nil
	}
	success := []byte(`{"type":"session.complete","data":{"object":{"sessionId":"sess_test","merchantReferenceId":"CLINK-test","paymentStatus":"paid"}}}`)
	require.NoError(t, handleClinkWebhook(c, success))
	require.NoError(t, handleClinkWebhook(c, success))
	require.NoError(t, handleClinkWebhook(c, failure))
	_, err := confirmClinkSession(c, "sess_test", "CLINK-test", 12833)
	require.NoError(t, err)
	require.Equal(t, 3, calls)
	var user model.User
	require.NoError(t, db.First(&user, 12833).Error)
	require.Equal(t, int(common.QuotaPerUnit), user.Quota)
	require.NoError(t, db.First(&current, order.Id).Error)
	require.Equal(t, "success", current.Status)
	var logs int64
	require.NoError(t, db.Model(&model.Log{}).Where("type = ?", model.LogTypeTopup).Count(&logs).Error)
	require.EqualValues(t, 1, logs)
}

func TestClinkConfirmationRejectsUnverifiedRecovery(t *testing.T) {
	for _, name := range []string{"unpaid", "wrong session", "wrong reference", "wrong currency", "wrong amount", "wrong user", "wrong provider", "upstream unavailable", "refunded"} {
		t.Run(name, func(t *testing.T) {
			db := setupCryptoPersistenceTestDB(t)
			require.NoError(t, db.AutoMigrate(&model.SubscriptionOrder{}))
			require.NoError(t, db.Create(&model.User{Id: 12833, Username: "clink-guard"}).Error)
			order := model.TopUp{UserId: 12833, TradeNo: "CLINK-test", Status: "failed", Amount: 1, Money: 1, PaymentProvider: "clink", PaymentMethod: "clink"}
			if name == "wrong provider" {
				order.PaymentProvider = "paypal"
			}
			if name == "refunded" {
				order.Status = "refunded"
			}
			require.NoError(t, db.Create(&order).Error)
			oldQuery, oldCurrency := getClinkSessionForConfirmation, setting.ClinkCurrency
			setting.ClinkCurrency = "USD"
			t.Cleanup(func() { getClinkSessionForConfirmation = oldQuery; setting.ClinkCurrency = oldCurrency })
			getClinkSessionForConfirmation = func(ctx context.Context, id string) (*service.ClinkCheckoutSessionDetail, error) {
				if name == "upstream unavailable" {
					return nil, errors.New("timeout")
				}
				s := &service.ClinkCheckoutSessionDetail{SessionID: id, PaymentStatus: "paid", MerchantReferenceID: "CLINK-test", OriginalCurrency: "USD", AmountSubtotal: 1}
				switch name {
				case "unpaid":
					s.PaymentStatus = "unpaid"
				case "wrong session":
					s.SessionID = "another"
				case "wrong reference":
					s.MerchantReferenceID = "another"
				case "wrong currency":
					s.OriginalCurrency = "VND"
				case "wrong amount":
					s.AmountSubtotal = 2
				}
				return s, nil
			}
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", "/", strings.NewReader("{}"))
			userID := 12833
			if name == "wrong user" {
				userID = 9
			}
			_, err := confirmClinkSession(c, "sess_test", "CLINK-test", userID)
			require.Error(t, err)
			var user model.User
			require.NoError(t, db.First(&user, 12833).Error)
			require.Zero(t, user.Quota)
			var current model.TopUp
			require.NoError(t, db.First(&current, order.Id).Error)
			require.Equal(t, order.Status, current.Status)
		})
	}
}

func TestClinkPendingOrderSuccessAfterFailedAttempt(t *testing.T) {
	db := setupCryptoPersistenceTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.SubscriptionOrder{}))
	require.NoError(t, db.Create(&model.User{Id: 12833, Username: "clink-order"}).Error)
	require.NoError(t, db.Create(&model.TopUp{UserId: 12833, TradeNo: "CLINK-test", Status: "pending", Amount: 1, Money: 1, PaymentProvider: "clink", PaymentMethod: "clink"}).Error)
	old := setting.ClinkCurrency
	setting.ClinkCurrency = "USD"
	t.Cleanup(func() { setting.ClinkCurrency = old })
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/", nil)
	failed := []byte(`{"type":"order.failed","data":{"merchantReferenceId":"CLINK-test"}}`)
	paid := []byte(`{"type":"order.succeeded","data":{"merchantReferenceId":"CLINK-test","status":"success","originalCurrency":"USD","paymentCurrency":"VND","amountSubtotal":1,"amountTotal":26989}}`)
	require.NoError(t, handleClinkWebhook(c, failed))
	require.NoError(t, handleClinkWebhook(c, paid))
	require.NoError(t, handleClinkWebhook(c, failed))
	require.NoError(t, handleClinkWebhook(c, paid))
	var user model.User
	require.NoError(t, db.First(&user, 12833).Error)
	require.Equal(t, int(common.QuotaPerUnit), user.Quota)
}

func TestClinkAdminReconciliationIsIdempotent(t *testing.T) {
	db := setupCryptoPersistenceTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.SubscriptionOrder{}))
	require.NoError(t, db.Create(&model.User{Id: 12833, Username: "clink-admin-repair"}).Error)
	require.NoError(t, db.Create(&model.TopUp{UserId: 12833, TradeNo: "CLINK-test", Status: "failed", Amount: 1, Money: 1, PaymentProvider: "clink", PaymentMethod: "clink"}).Error)
	oldQuery, oldCurrency := getClinkSessionForConfirmation, setting.ClinkCurrency
	setting.ClinkCurrency = "USD"
	t.Cleanup(func() { getClinkSessionForConfirmation = oldQuery; setting.ClinkCurrency = oldCurrency })
	calls := 0
	getClinkSessionForConfirmation = func(ctx context.Context, id string) (*service.ClinkCheckoutSessionDetail, error) {
		calls++
		return &service.ClinkCheckoutSessionDetail{SessionID: id, PaymentStatus: "paid", MerchantReferenceID: "CLINK-test", OriginalCurrency: "USD", AmountSubtotal: 1}, nil
	}
	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Set("id", 1)
		c.Request = httptest.NewRequest("POST", "/api/user/topup/clink/reconcile", strings.NewReader(`{"trade_no":"CLINK-test","session_id":"sess_test"}`))
		c.Request.Header.Set("Content-Type", "application/json")
		AdminReconcileClinkTopUp(c)
		require.Contains(t, w.Body.String(), `"success":true`)
	}
	require.Equal(t, 2, calls)
	var user model.User
	require.NoError(t, db.First(&user, 12833).Error)
	require.Equal(t, int(common.QuotaPerUnit), user.Quota)
	var logs int64
	require.NoError(t, db.Model(&model.Log{}).Where("type = ?", model.LogTypeTopup).Count(&logs).Error)
	require.EqualValues(t, 1, logs)
}
