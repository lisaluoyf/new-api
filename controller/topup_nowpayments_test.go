package controller

import (
	"context"
	"errors"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupNowPaymentsSettlementTest(t *testing.T, status string) (*model.NowPaymentsPayment, *model.TopUp, *model.User) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.TopUp{}, &model.NowPaymentsPayment{}, &model.Log{}, &model.TrialLimitNotification{}))
	previousDB := model.DB
	previousLogDB := model.LOG_DB
	previousGetter := getNowPaymentsPayment
	previousRedisEnabled := common.RedisEnabled
	previousShortfallPercent := setting.NowPaymentsPaymentShortfallPercent
	model.DB = db
	model.LOG_DB = db
	common.RedisEnabled = false
	setting.NowPaymentsPaymentShortfallPercent = 3
	t.Setenv("FEISHU_OPS_CHAT_ID", "")
	t.Setenv("GA_MP_API_SECRET", "")
	t.Cleanup(func() {
		model.DB = previousDB
		model.LOG_DB = previousLogDB
		getNowPaymentsPayment = previousGetter
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
		PaymentID: "invoice:987", PaymentStatus: "waiting",
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

func TestSettleNowPaymentsNonTerminalStaysPending(t *testing.T) {
	payment, topUp, user := setupNowPaymentsSettlementTest(t, "waiting")
	require.NoError(t, settleNowPaymentsPayment(payment, "12345", "127.0.0.1"))
	require.Equal(t, common.TopUpStatusPending, model.GetTopUpByTradeNo(topUp.TradeNo).Status)
	var storedUser model.User
	require.NoError(t, model.DB.First(&storedUser, user.Id).Error)
	require.Zero(t, storedUser.Quota)
}

func TestSettleNowPaymentsFailedMarksTopUpFailed(t *testing.T) {
	payment, topUp, user := setupNowPaymentsSettlementTest(t, "failed")
	require.NoError(t, settleNowPaymentsPayment(payment, "12345", "127.0.0.1"))
	require.Equal(t, common.TopUpStatusFailed, model.GetTopUpByTradeNo(topUp.TradeNo).Status)
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
	err := settleNowPaymentsPayment(payment, "12345", "127.0.0.1")
	require.ErrorIs(t, err, errNowPaymentsVerification)
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
	err := settleNowPaymentsPayment(payment, "12345", "127.0.0.1")
	require.ErrorIs(t, err, errNowPaymentsVerification)
	require.Equal(t, common.TopUpStatusPending, model.GetTopUpByTradeNo(topUp.TradeNo).Status)
	var storedUser model.User
	require.NoError(t, model.DB.First(&storedUser, user.Id).Error)
	require.Zero(t, storedUser.Quota)
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
