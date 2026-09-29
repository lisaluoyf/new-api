package controller

import (
	"context"
	"crypto/hmac"
	"crypto/sha512"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func setupNowPaymentsSettlementTest(t *testing.T, status string) (*model.NowPaymentsPayment, *model.TopUp, *model.User) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.TopUp{}, &model.NowPaymentsPayment{}, &model.NowPaymentsAttempt{}, &model.NowPaymentsReconcileState{}, &model.Log{}, &model.TrialLimitNotification{}))
	previousDB := model.DB
	previousLogDB := model.LOG_DB
	previousGetter := getNowPaymentsPayment
	previousAlert := sendNowPaymentsDuplicateAlert
	previousRedisEnabled := common.RedisEnabled
	previousShortfallPercent := setting.NowPaymentsPaymentShortfallPercent
	model.DB = db
	model.LOG_DB = db
	common.RedisEnabled = false
	setting.NowPaymentsPaymentShortfallPercent = 3
	t.Setenv("FEISHU_OPS_CHAT_ID", "")
	t.Setenv("FEISHU_APP_ID", "")
	t.Setenv("FEISHU_APP_SECRET", "")
	t.Setenv("GA_MP_API_SECRET", "")
	t.Cleanup(func() {
		model.DB = previousDB
		model.LOG_DB = previousLogDB
		getNowPaymentsPayment = previousGetter
		sendNowPaymentsDuplicateAlert = previousAlert
		common.RedisEnabled = previousRedisEnabled
		setting.NowPaymentsPaymentShortfallPercent = previousShortfallPercent
	})
	user := &model.User{Username: "nowpayments-test", Email: "nowpayments@example.com", Quota: 0}
	require.NoError(t, db.Create(user).Error)
	topUp := &model.TopUp{
		UserId: user.Id, Amount: 10, Money: 9, TradeNo: "NOWPAYMENTS-TEST",
		PaymentMethod: model.PaymentMethodNowPayments, PaymentProvider: model.PaymentProviderNowPayments,
		Status: common.TopUpStatusPending,
	}
	require.NoError(t, db.Create(topUp).Error)
	payment := &model.NowPaymentsPayment{
		TopUpTradeNo: topUp.TradeNo, InvoiceID: "987", InvoiceURL: "https://nowpayments.io/payment/?iid=987",
		PaymentID: "first-payment", PaymentStatus: "waiting",
	}
	require.NoError(t, db.Create(payment).Error)
	getNowPaymentsPayment = func(_ context.Context, paymentID string) (*service.NowPaymentsPaymentResponse, error) {
		if paymentID == "rpc-error" {
			return nil, errors.New("rpc unavailable")
		}
		return &service.NowPaymentsPaymentResponse{
			PaymentID: dto.StringValue(paymentID), InvoiceID: dto.StringValue("987"), PaymentStatus: status,
			PriceAmount: 9, PriceCurrency: "usd", PayAmount: 100, ActuallyPaid: 100,
			PayCurrency: "usdttrc20", Network: "trx", PayAddress: "TReceiver", PayinHash: "hash-1",
			OrderID: topUp.TradeNo,
		}, nil
	}
	return payment, topUp, user
}

func TestSettleNowPaymentsFinishedIsIdempotent(t *testing.T) {
	payment, topUp, user := setupNowPaymentsSettlementTest(t, "finished")
	require.NoError(t, settleNowPaymentsPayment(payment, "12345", "127.0.0.1"))
	firstQuota := int64(10 * common.QuotaPerUnit)
	var storedUser model.User
	require.NoError(t, model.DB.First(&storedUser, user.Id).Error)
	require.Equal(t, int(firstQuota), storedUser.Quota)
	require.Equal(t, common.TopUpStatusSuccess, model.GetTopUpByTradeNo(topUp.TradeNo).Status)

	require.NoError(t, settleNowPaymentsPayment(payment, "12345", "127.0.0.1"))
	require.NoError(t, model.DB.First(&storedUser, user.Id).Error)
	require.Equal(t, int(firstQuota), storedUser.Quota)
}

func TestSettleNowPaymentsAcceptsBoundPaymentWithoutOrderID(t *testing.T) {
	payment, topUp, user := setupNowPaymentsSettlementTest(t, "finished")
	require.NoError(t, settleNowPaymentsPayment(payment, "first-payment", "127.0.0.1"))
	getNowPaymentsPayment = func(_ context.Context, paymentID string) (*service.NowPaymentsPaymentResponse, error) {
		return &service.NowPaymentsPaymentResponse{
			PaymentID: dto.StringValue(paymentID), InvoiceID: dto.StringValue("987"), PaymentStatus: "finished",
			PriceAmount: 9, PriceCurrency: "usd", PayAmount: 100, ActuallyPaid: 100,
			PayCurrency: "usdttrc20", Network: "trx",
		}, nil
	}
	require.NoError(t, settleNowPaymentsPayment(payment, "first-payment", "127.0.0.1"))
	require.Equal(t, common.TopUpStatusSuccess, model.GetTopUpByTradeNo(topUp.TradeNo).Status)
	var storedUser model.User
	require.NoError(t, model.DB.First(&storedUser, user.Id).Error)
	require.Equal(t, int(10*common.QuotaPerUnit), storedUser.Quota)
}

func TestSettleNowPaymentsNonTerminalStaysPending(t *testing.T) {
	payment, topUp, user := setupNowPaymentsSettlementTest(t, "waiting")
	require.NoError(t, settleNowPaymentsPayment(payment, "12345", "127.0.0.1"))
	require.Equal(t, common.TopUpStatusPending, model.GetTopUpByTradeNo(topUp.TradeNo).Status)
	require.Equal(t, "12345", model.GetNowPaymentsPaymentByTradeNo(topUp.TradeNo).PaymentID)
	var storedUser model.User
	require.NoError(t, model.DB.First(&storedUser, user.Id).Error)
	require.Zero(t, storedUser.Quota)
}

func TestSettleNowPaymentsRejectsUnlinkedProviderPayment(t *testing.T) {
	payment, topUp, user := setupNowPaymentsSettlementTest(t, "finished")
	getNowPaymentsPayment = func(_ context.Context, paymentID string) (*service.NowPaymentsPaymentResponse, error) {
		return &service.NowPaymentsPaymentResponse{
			PaymentID: dto.StringValue(paymentID), InvoiceID: dto.StringValue("987"), PaymentStatus: "finished",
			PriceAmount: 9, PriceCurrency: "usd", PayAmount: 100, ActuallyPaid: 100,
			PayCurrency: "usdttrc20", Network: "trx",
		}, nil
	}
	err := settleNowPaymentsPayment(payment, "unlinked-payment", "127.0.0.1")
	require.ErrorIs(t, err, errNowPaymentsVerification)
	require.Equal(t, common.TopUpStatusPending, model.GetTopUpByTradeNo(topUp.TradeNo).Status)
	var storedUser model.User
	require.NoError(t, model.DB.First(&storedUser, user.Id).Error)
	require.Zero(t, storedUser.Quota)
}

func TestSettleNowPaymentsFailedAttemptKeepsTopUpPending(t *testing.T) {
	payment, topUp, user := setupNowPaymentsSettlementTest(t, "failed")
	require.NoError(t, settleNowPaymentsPayment(payment, "12345", "127.0.0.1"))
	require.Equal(t, common.TopUpStatusPending, model.GetTopUpByTradeNo(topUp.TradeNo).Status)
	require.Equal(t, "failed", model.GetNowPaymentsAttemptByID("12345").PaymentStatus)
	var storedUser model.User
	require.NoError(t, model.DB.First(&storedUser, user.Id).Error)
	require.Zero(t, storedUser.Quota)
}

func TestSettleNowPaymentsChangedCurrencyAfterExpiry(t *testing.T) {
	payment, topUp, user := setupNowPaymentsSettlementTest(t, "expired")
	require.NoError(t, settleNowPaymentsPayment(payment, "old-payment", "127.0.0.1"))
	getNowPaymentsPayment = func(_ context.Context, paymentID string) (*service.NowPaymentsPaymentResponse, error) {
		status := "finished"
		if paymentID == "old-payment" {
			status = "expired"
		}
		return &service.NowPaymentsPaymentResponse{
			PaymentID: dto.StringValue(paymentID), InvoiceID: dto.StringValue("987"), PaymentStatus: status,
			PriceAmount: 9, PriceCurrency: "usd", PayAmount: 9, ActuallyPaid: 9,
			PayCurrency: "usdterc20", Network: "eth", PayAddress: "new-address", PayinHash: "new-hash", OrderID: topUp.TradeNo,
		}, nil
	}
	require.NoError(t, settleNowPaymentsPayment(payment, "new-payment", "127.0.0.1"))
	require.Equal(t, common.TopUpStatusSuccess, model.GetTopUpByTradeNo(topUp.TradeNo).Status)
	require.Equal(t, "new-payment", model.GetNowPaymentsPaymentByTradeNo(topUp.TradeNo).PaymentID)
	require.Len(t, model.GetNowPaymentsAttemptsByOrder(topUp.TradeNo), 2)
	var storedUser model.User
	require.NoError(t, model.DB.First(&storedUser, user.Id).Error)
	require.Equal(t, int(10*common.QuotaPerUnit), storedUser.Quota)
	require.NoError(t, settleNowPaymentsPayment(payment, "old-payment", "127.0.0.1"))
	require.Equal(t, "new-payment", model.GetNowPaymentsPaymentByTradeNo(topUp.TradeNo).PaymentID)
}

func TestSettleNowPaymentsAnotherSuccessfulPaymentAlertsOnce(t *testing.T) {
	payment, topUp, user := setupNowPaymentsSettlementTest(t, "finished")
	require.NoError(t, settleNowPaymentsPayment(payment, "first-payment", "127.0.0.1"))
	getNowPaymentsPayment = func(_ context.Context, paymentID string) (*service.NowPaymentsPaymentResponse, error) {
		return &service.NowPaymentsPaymentResponse{
			PaymentID: dto.StringValue(paymentID), ParentPaymentID: dto.StringValue("first-payment"), InvoiceID: dto.StringValue("987"), PaymentStatus: "finished",
			PriceAmount: 9, PriceCurrency: "usd", PayAmount: 100, ActuallyPaid: 100,
			PayCurrency: "usdttrc20", Network: "trx", PayAddress: "TReceiver", PayinHash: "hash-2", OrderID: topUp.TradeNo,
		}, nil
	}
	t.Setenv("FEISHU_OPS_CHAT_ID", "ops-group-one")
	t.Setenv("FEISHU_APP_ID", "test-app")
	t.Setenv("FEISHU_APP_SECRET", "test-secret")
	alertCount := 0
	sendNowPaymentsDuplicateAlert = func(chatID, title string, lines []string) error {
		require.Equal(t, "ops-group-one", chatID)
		require.Contains(t, title, "重复付款告警")
		require.Contains(t, lines, "重复成功 Payment ID：second-payment")
		alertCount++
		return nil
	}
	require.NoError(t, settleNowPaymentsPayment(payment, "second-payment", "127.0.0.1"))
	require.NoError(t, settleNowPaymentsPayment(payment, "second-payment", "127.0.0.1"))
	require.Equal(t, 1, alertCount)
	require.NotZero(t, model.GetNowPaymentsAttemptByID("second-payment").DuplicateAlertedAt)
	require.Equal(t, "first-payment", model.GetNowPaymentsPaymentByTradeNo(topUp.TradeNo).PaymentID)
	var storedUser model.User
	require.NoError(t, model.DB.First(&storedUser, user.Id).Error)
	require.Equal(t, int(10*common.QuotaPerUnit), storedUser.Quota)
}

func TestSettleNowPaymentsRejectsRepeatedPaymentBeforeParentSettles(t *testing.T) {
	payment, topUp, user := setupNowPaymentsSettlementTest(t, "finished")
	getNowPaymentsPayment = func(_ context.Context, paymentID string) (*service.NowPaymentsPaymentResponse, error) {
		return &service.NowPaymentsPaymentResponse{
			PaymentID: dto.StringValue(paymentID), ParentPaymentID: dto.StringValue("first-payment"), InvoiceID: dto.StringValue("987"), PaymentStatus: "finished",
			PriceAmount: 9, PriceCurrency: "usd", PayAmount: 100, ActuallyPaid: 100,
			PayCurrency: "usdttrc20", Network: "trx", OrderID: topUp.TradeNo,
		}, nil
	}
	require.ErrorIs(t, settleNowPaymentsPayment(payment, "second-payment", "127.0.0.1"), errNowPaymentsVerification)
	require.Equal(t, common.TopUpStatusPending, model.GetTopUpByTradeNo(topUp.TradeNo).Status)
	var storedUser model.User
	require.NoError(t, model.DB.First(&storedUser, user.Id).Error)
	require.Zero(t, storedUser.Quota)
}

func TestSettleNowPaymentsAlertFailureRetries(t *testing.T) {
	payment, _, _ := setupNowPaymentsSettlementTest(t, "finished")
	require.NoError(t, settleNowPaymentsPayment(payment, "first-payment", "127.0.0.1"))
	t.Setenv("FEISHU_OPS_CHAT_ID", "ops-group-one")
	t.Setenv("FEISHU_APP_ID", "test-app")
	t.Setenv("FEISHU_APP_SECRET", "test-secret")
	alertCount := 0
	sendNowPaymentsDuplicateAlert = func(_, _ string, _ []string) error {
		alertCount++
		if alertCount == 1 {
			return fmt.Errorf("temporary Feishu error")
		}
		return nil
	}
	require.Error(t, settleNowPaymentsPayment(payment, "second-payment", "127.0.0.1"))
	require.Zero(t, model.GetNowPaymentsAttemptByID("second-payment").DuplicateAlertedAt)
	require.NoError(t, settleNowPaymentsPayment(payment, "second-payment", "127.0.0.1"))
	require.Equal(t, 2, alertCount)
}

func TestSettleNowPaymentsPreviouslyFailedOrderCanSettle(t *testing.T) {
	payment, topUp, user := setupNowPaymentsSettlementTest(t, "finished")
	require.NoError(t, model.DB.Model(topUp).Update("status", common.TopUpStatusFailed).Error)
	require.NoError(t, model.DB.Model(payment).Updates(map[string]any{"payment_status": "expired", "provider_payload": "verified"}).Error)
	require.NoError(t, settleNowPaymentsPayment(payment, "new-payment", "127.0.0.1"))
	require.Equal(t, common.TopUpStatusSuccess, model.GetTopUpByTradeNo(topUp.TradeNo).Status)
	var storedUser model.User
	require.NoError(t, model.DB.First(&storedUser, user.Id).Error)
	require.Equal(t, int(10*common.QuotaPerUnit), storedUser.Quota)
}

func TestSettleNowPaymentsUnverifiedFailedOrderDoesNotSettle(t *testing.T) {
	payment, topUp, user := setupNowPaymentsSettlementTest(t, "finished")
	require.NoError(t, model.DB.Model(topUp).Update("status", common.TopUpStatusFailed).Error)
	require.ErrorIs(t, settleNowPaymentsPayment(payment, "new-payment", "127.0.0.1"), errNowPaymentsVerification)
	require.Equal(t, common.TopUpStatusFailed, model.GetTopUpByTradeNo(topUp.TradeNo).Status)
	var storedUser model.User
	require.NoError(t, model.DB.First(&storedUser, user.Id).Error)
	require.Zero(t, storedUser.Quota)
}

func TestSettleNowPaymentsRejectsOrderChangedDuringVerification(t *testing.T) {
	payment, topUp, user := setupNowPaymentsSettlementTest(t, "finished")
	getNowPaymentsPayment = func(_ context.Context, paymentID string) (*service.NowPaymentsPaymentResponse, error) {
		require.NoError(t, model.DB.Model(topUp).Update("money", 10).Error)
		return &service.NowPaymentsPaymentResponse{
			PaymentID: dto.StringValue(paymentID), InvoiceID: dto.StringValue("987"), PaymentStatus: "finished",
			PriceAmount: 9, PriceCurrency: "usd", PayAmount: 9, ActuallyPaid: 9, OrderID: topUp.TradeNo,
		}, nil
	}
	require.ErrorIs(t, settleNowPaymentsPayment(payment, "new-payment", "127.0.0.1"), errNowPaymentsVerification)
	require.Equal(t, common.TopUpStatusPending, model.GetTopUpByTradeNo(topUp.TradeNo).Status)
	var storedUser model.User
	require.NoError(t, model.DB.First(&storedUser, user.Id).Error)
	require.Zero(t, storedUser.Quota)
}

func TestSettleNowPaymentsRejectsUnderpayment(t *testing.T) {
	payment, topUp, user := setupNowPaymentsSettlementTest(t, "finished")
	getNowPaymentsPayment = func(_ context.Context, paymentID string) (*service.NowPaymentsPaymentResponse, error) {
		return &service.NowPaymentsPaymentResponse{
			PaymentID: dto.StringValue(paymentID), InvoiceID: dto.StringValue("987"), PaymentStatus: "finished",
			PriceAmount: 9, PriceCurrency: "usd", PayAmount: 100, ActuallyPaid: 96.99,
			PayCurrency: "usdttrc20", Network: "trx", OrderID: topUp.TradeNo,
		}, nil
	}
	require.NoError(t, settleNowPaymentsPayment(payment, "12345", "127.0.0.1"))
	require.Equal(t, "finished", model.GetNowPaymentsAttemptByID("12345").PaymentStatus)
	require.True(t, model.GetNowPaymentsAttemptByID("12345").Underpaid)
	require.Equal(t, common.TopUpStatusPending, model.GetTopUpByTradeNo(topUp.TradeNo).Status)
	var storedUser model.User
	require.NoError(t, model.DB.First(&storedUser, user.Id).Error)
	require.Zero(t, storedUser.Quota)
}

func TestSettleNowPaymentsAcceptsThreePercentShortfall(t *testing.T) {
	payment, topUp, user := setupNowPaymentsSettlementTest(t, "partially_paid")
	getNowPaymentsPayment = func(_ context.Context, paymentID string) (*service.NowPaymentsPaymentResponse, error) {
		return &service.NowPaymentsPaymentResponse{
			PaymentID: dto.StringValue(paymentID), InvoiceID: dto.StringValue("987"), PaymentStatus: "partially_paid",
			PriceAmount: 9, PriceCurrency: "usd", PayAmount: 100, ActuallyPaid: 97,
			PayCurrency: "usdttrc20", Network: "trx", OrderID: topUp.TradeNo,
		}, nil
	}
	require.NoError(t, settleNowPaymentsPayment(payment, "12345", "127.0.0.1"))
	require.Equal(t, common.TopUpStatusSuccess, model.GetTopUpByTradeNo(topUp.TradeNo).Status)
	var storedUser model.User
	require.NoError(t, model.DB.First(&storedUser, user.Id).Error)
	require.Equal(t, int64(10*common.QuotaPerUnit), int64(storedUser.Quota))
}

func TestSettleNowPaymentsRejectsShortfallAboveThreePercent(t *testing.T) {
	payment, topUp, user := setupNowPaymentsSettlementTest(t, "partially_paid")
	getNowPaymentsPayment = func(_ context.Context, paymentID string) (*service.NowPaymentsPaymentResponse, error) {
		return &service.NowPaymentsPaymentResponse{
			PaymentID: dto.StringValue(paymentID), InvoiceID: dto.StringValue("987"), PaymentStatus: "partially_paid",
			PriceAmount: 9, PriceCurrency: "usd", PayAmount: 100, ActuallyPaid: 96.99,
			PayCurrency: "usdttrc20", Network: "trx", OrderID: topUp.TradeNo,
		}, nil
	}
	require.NoError(t, settleNowPaymentsPayment(payment, "12345", "127.0.0.1"))
	require.Equal(t, "partially_paid", model.GetNowPaymentsAttemptByID("12345").PaymentStatus)
	require.True(t, model.GetNowPaymentsAttemptByID("12345").Underpaid)
	require.Equal(t, common.TopUpStatusPending, model.GetTopUpByTradeNo(topUp.TradeNo).Status)
	var storedUser model.User
	require.NoError(t, model.DB.First(&storedUser, user.Id).Error)
	require.Zero(t, storedUser.Quota)
	getNowPaymentsPayment = func(_ context.Context, paymentID string) (*service.NowPaymentsPaymentResponse, error) {
		return &service.NowPaymentsPaymentResponse{
			PaymentID: dto.StringValue(paymentID), InvoiceID: dto.StringValue("987"), PaymentStatus: "finished",
			PriceAmount: 9, PriceCurrency: "usd", PayAmount: 100, ActuallyPaid: 100,
			PayCurrency: "usdttrc20", Network: "trx", OrderID: topUp.TradeNo,
		}, nil
	}
	require.NoError(t, settleNowPaymentsPayment(payment, "12345", "127.0.0.1"))
	require.Equal(t, common.TopUpStatusSuccess, model.GetTopUpByTradeNo(topUp.TradeNo).Status)
	require.False(t, model.GetNowPaymentsAttemptByID("12345").Underpaid)
	require.NoError(t, model.DB.First(&storedUser, user.Id).Error)
	require.Equal(t, int(10*common.QuotaPerUnit), storedUser.Quota)
}

func TestSettleNowPaymentsRejectsInvoiceMismatch(t *testing.T) {
	payment, topUp, _ := setupNowPaymentsSettlementTest(t, "finished")
	getNowPaymentsPayment = func(_ context.Context, paymentID string) (*service.NowPaymentsPaymentResponse, error) {
		return &service.NowPaymentsPaymentResponse{
			PaymentID: dto.StringValue(paymentID), InvoiceID: dto.StringValue("other"), PaymentStatus: "finished",
			PriceAmount: 9, PriceCurrency: "usd", PayAmount: 100, ActuallyPaid: 100, OrderID: topUp.TradeNo,
		}, nil
	}
	err := settleNowPaymentsPayment(payment, "12345", "127.0.0.1")
	require.ErrorIs(t, err, errNowPaymentsVerification)
	require.Equal(t, common.TopUpStatusPending, model.GetTopUpByTradeNo(topUp.TradeNo).Status)
}

func TestSettleNowPaymentsRPCErrorStaysPending(t *testing.T) {
	payment, topUp, _ := setupNowPaymentsSettlementTest(t, "finished")
	err := settleNowPaymentsPayment(payment, "rpc-error", "127.0.0.1")
	require.EqualError(t, err, "rpc unavailable")
	require.Equal(t, common.TopUpStatusPending, model.GetTopUpByTradeNo(topUp.TradeNo).Status)
}

func TestNowPaymentsWebhookAcceptsNewPaymentWithoutInvoiceInCallback(t *testing.T) {
	_, topUp, user := setupNowPaymentsSettlementTest(t, "finished")
	previousSecret := setting.NowPaymentsIPNSecret
	setting.NowPaymentsIPNSecret = "nowpayments-webhook-test-secret"
	t.Cleanup(func() { setting.NowPaymentsIPNSecret = previousSecret })
	body := `{"order_id":"NOWPAYMENTS-TEST","payment_id":"new-payment","payment_status":"finished"}`
	mac := hmac.New(sha512.New, []byte(setting.NowPaymentsIPNSecret))
	_, _ = mac.Write([]byte(body))
	request := httptest.NewRequest(http.MethodPost, "/api/nowpayments/webhook", strings.NewReader(body))
	request.Header.Set("x-nowpayments-sig", hex.EncodeToString(mac.Sum(nil)))
	response := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(response)
	context.Request = request
	NowPaymentsWebhook(context)
	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, common.TopUpStatusSuccess, model.GetTopUpByTradeNo(topUp.TradeNo).Status)
	require.Equal(t, "new-payment", model.GetNowPaymentsPaymentByTradeNo(topUp.TradeNo).PaymentID)
	var storedUser model.User
	require.NoError(t, model.DB.First(&storedUser, user.Id).Error)
	require.Equal(t, int(10*common.QuotaPerUnit), storedUser.Quota)
}

func TestNowPaymentsWebhookFindsRepeatedPaymentByParent(t *testing.T) {
	payment, topUp, user := setupNowPaymentsSettlementTest(t, "finished")
	require.NoError(t, settleNowPaymentsPayment(payment, "first-payment", "127.0.0.1"))
	previousSecret := setting.NowPaymentsIPNSecret
	setting.NowPaymentsIPNSecret = "nowpayments-webhook-test-secret"
	t.Cleanup(func() { setting.NowPaymentsIPNSecret = previousSecret })
	t.Setenv("FEISHU_OPS_CHAT_ID", "ops-group-one")
	t.Setenv("FEISHU_APP_ID", "test-app")
	t.Setenv("FEISHU_APP_SECRET", "test-secret")
	sendNowPaymentsDuplicateAlert = func(_, _ string, _ []string) error { return nil }
	getNowPaymentsPayment = func(_ context.Context, paymentID string) (*service.NowPaymentsPaymentResponse, error) {
		return &service.NowPaymentsPaymentResponse{
			PaymentID: dto.StringValue(paymentID), ParentPaymentID: dto.StringValue("first-payment"), InvoiceID: dto.StringValue("987"), PaymentStatus: "finished",
			PriceAmount: 9, PriceCurrency: "usd", PayAmount: 100, ActuallyPaid: 100, PayCurrency: "usdttrc20", Network: "trx",
		}, nil
	}
	body := `{"parent_payment_id":"first-payment","payment_id":"second-payment","payment_status":"finished"}`
	mac := hmac.New(sha512.New, []byte(setting.NowPaymentsIPNSecret))
	_, _ = mac.Write([]byte(body))
	request := httptest.NewRequest(http.MethodPost, "/api/nowpayments/webhook", strings.NewReader(body))
	request.Header.Set("x-nowpayments-sig", hex.EncodeToString(mac.Sum(nil)))
	response := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(response)
	context.Request = request
	NowPaymentsWebhook(context)
	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, "first-payment", model.GetNowPaymentsPaymentByTradeNo(topUp.TradeNo).PaymentID)
	attempt := model.GetNowPaymentsAttemptByID("second-payment")
	require.NotNil(t, attempt)
	require.True(t, attempt.DuplicatePayment)
	var storedUser model.User
	require.NoError(t, model.DB.First(&storedUser, user.Id).Error)
	require.Equal(t, int(10*common.QuotaPerUnit), storedUser.Quota)
}

func TestNowPaymentsRecoverySettlesKnownPaymentWithoutWebhook(t *testing.T) {
	_, topUp, user := setupNowPaymentsSettlementTest(t, "finished")
	require.NoError(t, runNowPaymentsRecoveryOnce(context.Background()))
	require.Equal(t, common.TopUpStatusSuccess, model.GetTopUpByTradeNo(topUp.TradeNo).Status)
	require.Equal(t, "first-payment", model.GetNowPaymentsPaymentByTradeNo(topUp.TradeNo).PaymentID)
	var storedUser model.User
	require.NoError(t, model.DB.First(&storedUser, user.Id).Error)
	require.Equal(t, int(10*common.QuotaPerUnit), storedUser.Quota)
}

func TestNowPaymentsRecoveryFindsRepeatedPaymentByParent(t *testing.T) {
	payment, topUp, user := setupNowPaymentsSettlementTest(t, "finished")
	require.NoError(t, settleNowPaymentsPayment(payment, "first-payment", "127.0.0.1"))
	t.Setenv("FEISHU_OPS_CHAT_ID", "ops-group-one")
	t.Setenv("FEISHU_APP_ID", "test-app")
	t.Setenv("FEISHU_APP_SECRET", "test-secret")
	sendNowPaymentsDuplicateAlert = func(_, _ string, _ []string) error { return nil }
	getNowPaymentsPayment = func(_ context.Context, paymentID string) (*service.NowPaymentsPaymentResponse, error) {
		if paymentID == "first-payment" {
			return &service.NowPaymentsPaymentResponse{
				PaymentID: dto.StringValue(paymentID), InvoiceID: dto.StringValue("987"), OrderID: topUp.TradeNo, PaymentStatus: "finished",
				PriceAmount: 9, PriceCurrency: "usd", PayAmount: 100, ActuallyPaid: 100, PayCurrency: "usdttrc20", Network: "trx",
				PaymentExtraIDs: []dto.StringValue{dto.StringValue("second-payment")},
			}, nil
		}
		return &service.NowPaymentsPaymentResponse{
			PaymentID: dto.StringValue(paymentID), ParentPaymentID: dto.StringValue("first-payment"), InvoiceID: dto.StringValue("987"), PaymentStatus: "finished",
			PriceAmount: 9, PriceCurrency: "usd", PayAmount: 100, ActuallyPaid: 100, PayCurrency: "usdttrc20", Network: "trx",
		}, nil
	}
	require.NoError(t, runNowPaymentsRecoveryOnce(context.Background()))
	require.Equal(t, "first-payment", model.GetNowPaymentsPaymentByTradeNo(topUp.TradeNo).PaymentID)
	attempt := model.GetNowPaymentsAttemptByID("second-payment")
	require.NotNil(t, attempt)
	require.True(t, attempt.DuplicatePayment)
	var storedUser model.User
	require.NoError(t, model.DB.First(&storedUser, user.Id).Error)
	require.Equal(t, int(10*common.QuotaPerUnit), storedUser.Quota)
}

func TestNowPaymentsRecoveryRetriesClaimedAlert(t *testing.T) {
	payment, topUp, _ := setupNowPaymentsSettlementTest(t, "finished")
	require.NoError(t, settleNowPaymentsPayment(payment, "first-payment", "127.0.0.1"))
	require.Error(t, settleNowPaymentsPayment(payment, "second-payment", "127.0.0.1"))
	attempt := model.GetNowPaymentsAttemptByID("second-payment")
	require.True(t, attempt.DuplicatePayment)
	require.Zero(t, attempt.DuplicateAlertedAt)
	require.NoError(t, model.DB.Model(attempt).Update("alert_claim_until", common.GetTimestamp()-1).Error)
	t.Setenv("FEISHU_OPS_CHAT_ID", "ops-group-one")
	t.Setenv("FEISHU_APP_ID", "test-app")
	t.Setenv("FEISHU_APP_SECRET", "test-secret")
	alertCount := 0
	sendNowPaymentsDuplicateAlert = func(_, _ string, _ []string) error {
		alertCount++
		return nil
	}
	require.NoError(t, runNowPaymentsRecoveryOnce(context.Background()))
	require.Equal(t, 1, alertCount)
	require.NotZero(t, model.GetNowPaymentsAttemptByID("second-payment").DuplicateAlertedAt)
	require.Equal(t, "first-payment", model.GetNowPaymentsPaymentByTradeNo(topUp.TradeNo).PaymentID)
}

func TestNowPaymentsRecoveryReportsUnavailablePaymentLookup(t *testing.T) {
	_, topUp, user := setupNowPaymentsSettlementTest(t, "finished")
	getNowPaymentsPayment = func(_ context.Context, _ string) (*service.NowPaymentsPaymentResponse, error) {
		return nil, fmt.Errorf("NOWPayments payment lookup returned 401")
	}
	require.Error(t, runNowPaymentsRecoveryOnce(context.Background()))
	require.Equal(t, common.TopUpStatusPending, model.GetTopUpByTradeNo(topUp.TradeNo).Status)
	var storedUser model.User
	require.NoError(t, model.DB.First(&storedUser, user.Id).Error)
	require.Zero(t, storedUser.Quota)
}

func TestNowPaymentsRecoveryAlertRetriesAndThrottles(t *testing.T) {
	setupNowPaymentsSettlementTest(t, "waiting")
	t.Setenv("FEISHU_OPS_CHAT_ID", "ops-group-one")
	t.Setenv("FEISHU_APP_ID", "test-app")
	t.Setenv("FEISHU_APP_SECRET", "test-secret")
	getNowPaymentsPayment = func(_ context.Context, _ string) (*service.NowPaymentsPaymentResponse, error) {
		return nil, fmt.Errorf("NOWPayments payment lookup returned 401")
	}
	alertCount := 0
	sendNowPaymentsDuplicateAlert = func(chatID, title string, _ []string) error {
		require.Equal(t, "ops-group-one", chatID)
		require.Contains(t, title, "自动对账异常")
		alertCount++
		if alertCount == 1 {
			return fmt.Errorf("Feishu temporarily unavailable")
		}
		return nil
	}
	runNowPaymentsRecovery()
	runNowPaymentsRecovery()
	runNowPaymentsRecovery()
	require.Equal(t, 2, alertCount)
}

func TestNowPaymentsRecoverySkipsPaymentsOlderThanSevenDays(t *testing.T) {
	payment, topUp, user := setupNowPaymentsSettlementTest(t, "finished")
	require.NoError(t, model.DB.Model(payment).Update("created_at", time.Now().Add(-8*24*time.Hour).Unix()).Error)
	lookupCount := 0
	getNowPaymentsPayment = func(_ context.Context, _ string) (*service.NowPaymentsPaymentResponse, error) {
		lookupCount++
		return nil, fmt.Errorf("should not be called")
	}
	require.NoError(t, runNowPaymentsRecoveryOnce(context.Background()))
	require.Zero(t, lookupCount)
	require.Equal(t, common.TopUpStatusPending, model.GetTopUpByTradeNo(topUp.TradeNo).Status)
	var storedUser model.User
	require.NoError(t, model.DB.First(&storedUser, user.Id).Error)
	require.Zero(t, storedUser.Quota)
}

func TestNowPaymentsPostgresConcurrentSettlement(t *testing.T) {
	dsn := os.Getenv("NOWPAYMENTS_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set NOWPAYMENTS_TEST_POSTGRES_DSN for PostgreSQL migration and concurrency coverage")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	for range 2 {
		require.NoError(t, db.AutoMigrate(&model.User{}, &model.TopUp{}, &model.NowPaymentsPayment{}, &model.NowPaymentsAttempt{}, &model.NowPaymentsReconcileState{}, &model.Log{}, &model.TrialLimitNotification{}))
	}
	previousDB, previousLogDB := model.DB, model.LOG_DB
	previousGetter, previousAlert := getNowPaymentsPayment, sendNowPaymentsDuplicateAlert
	previousTopupSucceeded := onNowPaymentsTopupSucceeded
	previousRedisEnabled := common.RedisEnabled
	model.DB, model.LOG_DB = db, db
	common.RedisEnabled = false
	onNowPaymentsTopupSucceeded = func(int, int, string, string) {}
	t.Setenv("FEISHU_OPS_CHAT_ID", "ops-group-one")
	t.Setenv("FEISHU_APP_ID", "test-app")
	t.Setenv("FEISHU_APP_SECRET", "test-secret")
	t.Setenv("GA_MP_API_SECRET", "")
	t.Cleanup(func() {
		model.DB, model.LOG_DB = previousDB, previousLogDB
		getNowPaymentsPayment, sendNowPaymentsDuplicateAlert = previousGetter, previousAlert
		onNowPaymentsTopupSucceeded = previousTopupSucceeded
		common.RedisEnabled = previousRedisEnabled
	})
	uniqueID := fmt.Sprintf("%d", time.Now().UnixNano())
	user := &model.User{Username: "pg-nowpayments-" + uniqueID, Email: "pg-nowpayments-" + uniqueID + "@example.com", AffCode: "pg-" + uniqueID}
	require.NoError(t, db.Create(user).Error)
	order := &model.TopUp{UserId: user.Id, Amount: 10, Money: 9, TradeNo: "NOWPAYMENTS-PG-TEST-" + uniqueID, PaymentMethod: model.PaymentMethodNowPayments, PaymentProvider: model.PaymentProviderNowPayments, Status: common.TopUpStatusPending}
	require.NoError(t, db.Create(order).Error)
	invoiceID := "pg-invoice-" + uniqueID
	local := &model.NowPaymentsPayment{TopUpTradeNo: order.TradeNo, InvoiceID: invoiceID, PaymentID: "invoice:" + invoiceID, PaymentStatus: "waiting"}
	require.NoError(t, db.Create(local).Error)
	getNowPaymentsPayment = func(_ context.Context, paymentID string) (*service.NowPaymentsPaymentResponse, error) {
		return &service.NowPaymentsPaymentResponse{PaymentID: dto.StringValue(paymentID), InvoiceID: dto.StringValue(invoiceID), OrderID: order.TradeNo, PriceAmount: 9, PriceCurrency: "usd", PayAmount: 9, ActuallyPaid: 9, PaymentStatus: "finished", PayCurrency: "usdttrc20"}, nil
	}
	var alertMu sync.Mutex
	alertCount := 0
	sendNowPaymentsDuplicateAlert = func(_, _ string, _ []string) error {
		alertMu.Lock()
		defer alertMu.Unlock()
		alertCount++
		return nil
	}
	var group sync.WaitGroup
	results := make(chan error, 2)
	for _, paymentID := range []string{"pg-payment-1-" + uniqueID, "pg-payment-2-" + uniqueID} {
		group.Add(1)
		go func() {
			defer group.Done()
			results <- settleNowPaymentsPayment(local, paymentID, "127.0.0.1")
		}()
	}
	group.Wait()
	close(results)
	for result := range results {
		require.NoError(t, result)
	}
	var storedUser model.User
	require.NoError(t, db.First(&storedUser, user.Id).Error)
	require.Equal(t, int(10*common.QuotaPerUnit), storedUser.Quota)
	require.Equal(t, common.TopUpStatusSuccess, model.GetTopUpByTradeNo(order.TradeNo).Status)
	require.Len(t, model.GetNowPaymentsAttemptsByOrder(order.TradeNo), 2)
	alertMu.Lock()
	require.Equal(t, 1, alertCount)
	alertMu.Unlock()
}
