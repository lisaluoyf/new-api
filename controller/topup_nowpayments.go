package controller

import (
	"context"
	"errors"
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

var errNowPaymentsVerification = errors.New("NOWPayments payment verification failed")
var getNowPaymentsPayment = service.GetNowPaymentsPayment

type NowPaymentsPayRequest struct {
	Amount int64 `json:"amount"`
}

func RequestNowPaymentsPay(c *gin.Context) {
	if abortIfTopupForbidden(c) || !isNowPaymentsTopUpEnabled() {
		if !isCurrentUserTopupForbidden(c) {
			common.ApiErrorMsg(c, "NOWPayments is not enabled")
		}
		return
	}
	var req NowPaymentsPayRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.Amount <= 0 {
		common.ApiErrorMsg(c, "Invalid amount")
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
	baseURL := strings.TrimRight(system_setting.ServerAddress, "/")
	invoice, err := service.CreateNowPaymentsInvoice(c.Request.Context(), &service.NowPaymentsCreateInvoiceRequest{
		PriceAmount: payMoney, PriceCurrency: "usd", IPNCallbackURL: baseURL + "/api/nowpayments/webhook",
		OrderID: tradeNo, OrderDescription: "APIMaster wallet top-up",
		SuccessURL: baseURL + "/console/wallet?show_history=true", CancelURL: baseURL + "/console/wallet",
		PartiallyPaidURL: baseURL + "/console/wallet?show_history=true", IsFixedRate: true, IsFeePaidByUser: false,
	})
	if err != nil {
		_ = model.UpdatePendingTopUpStatus(tradeNo, model.PaymentProviderNowPayments, common.TopUpStatusFailed)
		logger.LogError(c.Request.Context(), fmt.Sprintf("create NOWPayments invoice failed: %v", err))
		common.ApiErrorMsg(c, "Failed to create cryptocurrency payment")
		return
	}
	invoiceID := strings.TrimSpace(string(invoice.ID))
	if invoice.OrderID != tradeNo || !strings.EqualFold(invoice.PriceCurrency, "usd") || decimal.NewFromFloat(invoice.PriceAmount).Sub(decimal.NewFromFloat(payMoney)).Abs().GreaterThan(decimal.NewFromFloat(0.005)) {
		_ = model.UpdatePendingTopUpStatus(tradeNo, model.PaymentProviderNowPayments, common.TopUpStatusFailed)
		common.ApiErrorMsg(c, "Cryptocurrency payment details mismatch")
		return
	}
	local := &model.NowPaymentsPayment{
		TopUpTradeNo: tradeNo, InvoiceID: invoiceID, InvoiceURL: invoice.InvoiceURL,
		PaymentID: "invoice:" + invoiceID, PaymentStatus: "waiting",
	}
	if err := local.Insert(); err != nil {
		_ = model.UpdatePendingTopUpStatus(tradeNo, model.PaymentProviderNowPayments, common.TopUpStatusFailed)
		logger.LogError(c.Request.Context(), fmt.Sprintf("save NOWPayments invoice failed: %v", err))
		common.ApiErrorMsg(c, "Failed to save cryptocurrency payment")
		return
	}
	common.ApiSuccess(c, gin.H{"invoice_id": invoiceID, "order_id": tradeNo, "invoice_url": invoice.InvoiceURL})
}

func settleNowPaymentsPayment(local *model.NowPaymentsPayment, paymentID, callerIP string) error {
	if local == nil || strings.TrimSpace(paymentID) == "" {
		return fmt.Errorf("%w: payment not found", errNowPaymentsVerification)
	}
	remote, err := getNowPaymentsPayment(context.Background(), paymentID)
	if err != nil {
		return err
	}
	remotePaymentID := strings.TrimSpace(string(remote.PaymentID))
	remoteInvoiceID := strings.TrimSpace(string(remote.InvoiceID))
	if remotePaymentID != strings.TrimSpace(paymentID) || string(remote.ParentPaymentID) != "" || remote.OrderID != local.TopUpTradeNo || !strings.EqualFold(remote.PriceCurrency, "usd") {
		return fmt.Errorf("%w: payment details mismatch", errNowPaymentsVerification)
	}
	if local.InvoiceID != "" && remoteInvoiceID != local.InvoiceID {
		return fmt.Errorf("%w: invoice id mismatch", errNowPaymentsVerification)
	}
	if local.InvoiceID == "" && (remoteInvoiceID != "" || remote.PayAddress != local.PayAddress || !strings.EqualFold(remote.PayCurrency, local.PayCurrency)) {
		return fmt.Errorf("%w: legacy payment details mismatch", errNowPaymentsVerification)
	}
	if local.PaymentID != "invoice:"+local.InvoiceID && local.PaymentID != remotePaymentID {
		return fmt.Errorf("%w: payment id mismatch", errNowPaymentsVerification)
	}
	topUp := model.GetTopUpByTradeNo(local.TopUpTradeNo)
	if topUp == nil || topUp.PaymentProvider != model.PaymentProviderNowPayments {
		return fmt.Errorf("%w: topup not found", errNowPaymentsVerification)
	}
	if decimal.NewFromFloat(remote.PriceAmount).Sub(decimal.NewFromFloat(topUp.Money)).Abs().GreaterThan(decimal.NewFromFloat(0.005)) {
		return fmt.Errorf("%w: payment amount mismatch", errNowPaymentsVerification)
	}
	legacyPayAmount := local.PayAmount
	local.PaymentID = remotePaymentID
	local.PayAddress = remote.PayAddress
	local.PayinExtraID = remote.PayinExtraID
	local.PayCurrency = remote.PayCurrency
	local.PayAmount = strconv.FormatFloat(remote.PayAmount, 'f', -1, 64)
	local.ActuallyPaid = strconv.FormatFloat(remote.ActuallyPaid, 'f', -1, 64)
	local.Network = remote.Network
	local.PaymentStatus = strings.ToLower(strings.TrimSpace(remote.PaymentStatus))
	local.PayinHash = remote.PayinHash
	local.ProviderPayload = "verified"
	local.ExpiresAt = service.ParseNowPaymentsExpiry(remote.ExpirationEstimateDate)
	if err := local.Update(); err != nil {
		return err
	}
	remoteStatus := local.PaymentStatus
	if remoteStatus == "failed" || remoteStatus == "expired" || remoteStatus == "refunded" {
		if err := model.UpdatePendingTopUpStatus(local.TopUpTradeNo, model.PaymentProviderNowPayments, common.TopUpStatusFailed); err != nil && !errors.Is(err, model.ErrTopUpStatusInvalid) {
			return err
		}
		return nil
	}
	if remoteStatus != "finished" {
		return nil
	}
	expectedCryptoAmount := decimal.NewFromFloat(remote.PayAmount)
	if local.InvoiceID == "" {
		legacyAmount, err := decimal.NewFromString(legacyPayAmount)
		if err != nil || expectedCryptoAmount.Sub(legacyAmount).Abs().GreaterThan(decimal.NewFromFloat(0.00000001)) {
			return fmt.Errorf("%w: legacy cryptocurrency amount mismatch", errNowPaymentsVerification)
		}
	}
	if expectedCryptoAmount.LessThanOrEqual(decimal.Zero) || decimal.NewFromFloat(remote.ActuallyPaid).Add(decimal.NewFromFloat(0.00000001)).LessThan(expectedCryptoAmount) {
		return fmt.Errorf("%w: cryptocurrency amount underpaid", errNowPaymentsVerification)
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
		InvoiceID     dto.StringValue `json:"invoice_id"`
		OrderID       string          `json:"order_id"`
		PaymentStatus string          `json:"payment_status"`
	}
	if err := common.Unmarshal(body, &payload); err != nil || strings.TrimSpace(string(payload.PaymentID)) == "" || strings.TrimSpace(payload.OrderID) == "" {
		c.Status(http.StatusBadRequest)
		return
	}
	var local *model.NowPaymentsPayment
	if strings.TrimSpace(string(payload.InvoiceID)) != "" {
		local = model.GetNowPaymentsPaymentByOrder(string(payload.InvoiceID), payload.OrderID)
	} else {
		local = model.GetNowPaymentsPaymentByID(string(payload.PaymentID))
		if local != nil && (local.InvoiceID != "" || local.TopUpTradeNo != strings.TrimSpace(payload.OrderID)) {
			local = nil
		}
	}
	if local == nil {
		c.Status(http.StatusBadRequest)
		return
	}
	if err := settleNowPaymentsPayment(local, string(payload.PaymentID), c.ClientIP()); err != nil {
		if errors.Is(err, errNowPaymentsVerification) {
			c.Status(http.StatusBadRequest)
			return
		}
		logger.LogError(c.Request.Context(), fmt.Sprintf("verify NOWPayments webhook failed: %v", err))
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
	local := model.GetNowPaymentsPaymentByInvoiceID(c.Param("id"))
	if local == nil {
		common.ApiErrorMsg(c, "Payment not found")
		return
	}
	topUp := model.GetTopUpByTradeNo(local.TopUpTradeNo)
	if topUp == nil || topUp.UserId != c.GetInt("id") {
		c.Status(http.StatusNotFound)
		return
	}
	common.ApiSuccess(c, gin.H{"invoice_id": local.InvoiceID, "payment_id": local.PaymentID, "payment_status": local.PaymentStatus, "payin_hash": local.PayinHash, "topup_status": topUp.Status})
}
