package controller

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"github.com/thanhpk/randstr"
	"gorm.io/gorm"
)

type NowPaymentsPayRequest struct {
	Amount      int64  `json:"amount"`
	PayCurrency string `json:"pay_currency"`
}

var supportedNowPaymentsCurrencies = map[string]bool{
	"trx": true, "usdttrc20": true, "sol": true, "usdtsol": true,
}

func RequestNowPaymentsPay(c *gin.Context) {
	if abortIfTopupForbidden(c) || !isNowPaymentsTopUpEnabled() {
		if !isCurrentUserTopupForbidden(c) {
			common.ApiErrorMsg(c, "NOWPayments is not enabled")
		}
		return
	}
	var req NowPaymentsPayRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.Amount <= 0 || !supportedNowPaymentsCurrencies[strings.ToLower(strings.TrimSpace(req.PayCurrency))] {
		common.ApiErrorMsg(c, "Invalid amount or cryptocurrency")
		return
	}
	userID := c.GetInt("id")
	minTopup := getWalletMinTopupForUser(userID, setting.NowPaymentsMinTopUp)
	if req.Amount < minTopup {
		common.ApiErrorMsg(c, fmt.Sprintf("Top-up amount cannot be less than %d", minTopup))
		return
	}
	user, err := model.GetUserById(userID, false)
	if err != nil || user == nil {
		common.ApiErrorMsg(c, "User does not exist")
		return
	}
	if !isNowPaymentsAllowedEmail(user.Email) {
		c.Status(http.StatusNotFound)
		return
	}
	group, err := model.GetUserGroup(userID, true)
	if err != nil {
		common.ApiErrorMsg(c, "Failed to get user group")
		return
	}
	payMoney := getPayMoney(req.Amount, group, userID)
	if payMoney <= 0.01 {
		common.ApiErrorMsg(c, "Top-up amount is too low")
		return
	}
	tradeNo := fmt.Sprintf("NOWPAYMENTS-%d-%d-%s", userID, time.Now().UnixMilli(), randstr.String(6))
	topUp := &model.TopUp{UserId: userID, Amount: req.Amount, PaidAmountUSD: payMoney, PaidAmountUSDSource: "order", Money: payMoney, TradeNo: tradeNo, PaymentMethod: model.PaymentMethodNowPayments, PaymentProvider: model.PaymentProviderNowPayments, CreateTime: time.Now().Unix(), Status: common.TopUpStatusPending}
	if err := topUp.FillCountryFromIP(c.ClientIP(), user.Country).Insert(); err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("create NOWPayments topup failed: %v", err))
		common.ApiErrorMsg(c, "Failed to create order")
		return
	}
	payment, err := service.CreateNowPaymentsPayment(c.Request.Context(), &service.NowPaymentsCreatePaymentRequest{
		PriceAmount: payMoney, PriceCurrency: "usd", PayCurrency: strings.ToLower(strings.TrimSpace(req.PayCurrency)),
		IPNCallbackURL: strings.TrimRight(system_setting.ServerAddress, "/") + "/api/nowpayments/webhook", OrderID: tradeNo,
		OrderDescription: "APIMaster wallet top-up", IsFixedRate: true, IsFeePaidByUser: false,
	})
	if err != nil {
		_ = model.UpdatePendingTopUpStatus(tradeNo, model.PaymentProviderNowPayments, common.TopUpStatusFailed)
		common.ApiErrorMsg(c, "Failed to create cryptocurrency payment")
		return
	}
	paymentID := string(payment.PaymentID)
	local := &model.NowPaymentsPayment{TopUpTradeNo: tradeNo, PaymentID: paymentID, PayAddress: payment.PayAddress, PayinExtraID: payment.PayinExtraID, PayCurrency: payment.PayCurrency, PayAmount: strconv.FormatFloat(payment.PayAmount, 'f', -1, 64), Network: payment.Network, PaymentStatus: payment.PaymentStatus, ExpiresAt: service.ParseNowPaymentsExpiry(payment.ExpirationEstimateDate)}
	if err := local.Insert(); err != nil {
		_ = model.UpdatePendingTopUpStatus(tradeNo, model.PaymentProviderNowPayments, common.TopUpStatusFailed)
		common.ApiErrorMsg(c, "Failed to save cryptocurrency payment")
		return
	}
	common.ApiSuccess(c, gin.H{"payment_id": paymentID, "order_id": tradeNo, "pay_address": payment.PayAddress, "payin_extra_id": payment.PayinExtraID, "pay_amount": payment.PayAmount, "pay_currency": payment.PayCurrency, "network": payment.Network, "payment_status": payment.PaymentStatus, "expires_at": local.ExpiresAt})
}

func settleNowPaymentsPayment(paymentID, callerIP string) error {
	local := model.GetNowPaymentsPaymentByID(paymentID)
	if local == nil {
		return fmt.Errorf("payment not found")
	}
	remote, err := service.GetNowPaymentsPayment(context.Background(), paymentID)
	if err != nil {
		return err
	}
	if string(remote.PaymentID) != local.PaymentID || string(remote.ParentPaymentID) != "" || remote.OrderID != local.TopUpTradeNo || remote.PayAddress != local.PayAddress || !strings.EqualFold(remote.PriceCurrency, "usd") || !strings.EqualFold(remote.PayCurrency, local.PayCurrency) {
		return fmt.Errorf("payment details mismatch")
	}
	topUp := model.GetTopUpByTradeNo(local.TopUpTradeNo)
	if topUp == nil || topUp.PaymentProvider != model.PaymentProviderNowPayments {
		return fmt.Errorf("topup not found")
	}
	if decimal.NewFromFloat(remote.PriceAmount).Sub(decimal.NewFromFloat(topUp.Money)).Abs().GreaterThan(decimal.NewFromFloat(0.005)) {
		return fmt.Errorf("payment amount mismatch")
	}
	expectedCryptoAmount, err := decimal.NewFromString(local.PayAmount)
	if err != nil || decimal.NewFromFloat(remote.PayAmount).Sub(expectedCryptoAmount).Abs().GreaterThan(decimal.NewFromFloat(0.00000001)) {
		return fmt.Errorf("cryptocurrency amount mismatch")
	}
	local.PaymentStatus, local.PayinHash, local.ProviderPayload = strings.ToLower(remote.PaymentStatus), remote.PayinHash, "verified"
	local.ExpiresAt = service.ParseNowPaymentsExpiry(remote.ExpirationEstimateDate)
	if err := local.Update(); err != nil {
		return err
	}
	remoteStatus := strings.ToLower(strings.TrimSpace(remote.PaymentStatus))
	if remoteStatus == "failed" || remoteStatus == "expired" || remoteStatus == "refunded" {
		if err := model.UpdatePendingTopUpStatus(local.TopUpTradeNo, model.PaymentProviderNowPayments, common.TopUpStatusFailed); err != nil && err != model.ErrTopUpStatusInvalid {
			return err
		}
		return nil
	}
	if remoteStatus != "finished" {
		return nil
	}
	if decimal.NewFromFloat(remote.ActuallyPaid).Add(decimal.NewFromFloat(0.00000001)).LessThan(expectedCryptoAmount) {
		return fmt.Errorf("cryptocurrency amount underpaid")
	}
	var quota int
	err = model.DB.Transaction(func(tx *gorm.DB) error {
		locked := &model.TopUp{}
		if err := tx.Set("gorm:query_option", "FOR UPDATE").Where("trade_no = ?", local.TopUpTradeNo).First(locked).Error; err != nil {
			return err
		}
		if locked.Status == common.TopUpStatusSuccess {
			return nil
		}
		if locked.Status != common.TopUpStatusPending {
			return fmt.Errorf("topup status invalid")
		}
		quota = int(decimal.NewFromInt(locked.Amount).Mul(decimal.NewFromFloat(common.QuotaPerUnit)).IntPart())
		if quota <= 0 {
			return fmt.Errorf("invalid quota")
		}
		model.MarkTopUpSuccess(locked)
		if err := tx.Save(locked).Error; err != nil {
			return err
		}
		return tx.Model(&model.User{}).Where("id = ?", locked.UserId).Update("quota", gorm.Expr("quota + ?", quota)).Error
	})
	if err != nil {
		return err
	}
	if quota > 0 {
		model.RecordTopupLog(topUp.UserId, fmt.Sprintf("NOWPayments top-up successful, credited amount: %v, amount paid: %.2f", logger.FormatQuota(quota), topUp.Money), callerIP, topUp.PaymentMethod, model.PaymentMethodNowPayments)
		model.OnTopupSucceeded(topUp.UserId, quota, model.PaymentMethodNowPayments, topUp.TradeNo)
	}
	return nil
}

func NowPaymentsWebhook(c *gin.Context) {
	body, err := io.ReadAll(c.Request.Body)
	if err != nil || !service.VerifyNowPaymentsIPN(body, c.GetHeader("x-nowpayments-sig")) {
		c.Status(http.StatusUnauthorized)
		return
	}
	var payload struct {
		PaymentID     dto.StringValue `json:"payment_id"`
		PaymentStatus string          `json:"payment_status"`
	}
	if err := common.Unmarshal(body, &payload); err != nil || strings.TrimSpace(string(payload.PaymentID)) == "" {
		c.Status(http.StatusBadRequest)
		return
	}
	if err := settleNowPaymentsPayment(string(payload.PaymentID), c.ClientIP()); err != nil {
		c.Status(http.StatusServiceUnavailable)
		return
	}
	c.Status(http.StatusOK)
}

func GetNowPaymentsPaymentStatus(c *gin.Context) {
	if !isNowPaymentsTopUpEnabled() {
		common.ApiErrorMsg(c, "NOWPayments is not enabled")
		return
	}
	local := model.GetNowPaymentsPaymentByID(c.Param("id"))
	if local == nil {
		common.ApiErrorMsg(c, "Payment not found")
		return
	}
	topUp := model.GetTopUpByTradeNo(local.TopUpTradeNo)
	if topUp == nil || topUp.UserId != c.GetInt("id") {
		c.Status(http.StatusNotFound)
		return
	}
	if err := settleNowPaymentsPayment(local.PaymentID, c.ClientIP()); err != nil {
		common.ApiErrorMsg(c, "Payment status is temporarily unavailable")
		return
	}
	local = model.GetNowPaymentsPaymentByID(local.PaymentID)
	topUp = model.GetTopUpByTradeNo(local.TopUpTradeNo)
	common.ApiSuccess(c, gin.H{"payment_id": local.PaymentID, "payment_status": local.PaymentStatus, "payin_hash": local.PayinHash, "topup_status": topUp.Status})
}
