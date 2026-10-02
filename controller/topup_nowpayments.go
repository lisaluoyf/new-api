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
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"github.com/thanhpk/randstr"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var errNowPaymentsVerification = errors.New("NOWPayments payment verification failed")
var getNowPaymentsPayment = service.GetNowPaymentsPayment
var sendNowPaymentsDuplicateAlert = common.SendFeishuCard
var onNowPaymentsTopupSucceeded = model.OnTopupSucceeded

func nowPaymentsPaymentCoversAmount(payAmount, actuallyPaid float64) bool {
	expected := decimal.NewFromFloat(payAmount)
	paid := decimal.NewFromFloat(actuallyPaid)
	if expected.LessThanOrEqual(decimal.Zero) || paid.LessThan(decimal.Zero) {
		return false
	}
	shortfall := expected.Sub(paid)
	if shortfall.LessThanOrEqual(decimal.Zero) {
		return true
	}
	tolerance := expected.Mul(decimal.NewFromFloat(setting.NowPaymentsPaymentShortfallPercent)).Div(decimal.NewFromInt(100))
	return shortfall.LessThanOrEqual(tolerance.Add(decimal.NewFromFloat(0.00000001)))
}

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
	// Hosted Checkout is priced in USD. Keep the requested top-up amount as
	// the credited amount, and apply only the explicit top-up/promo discounts
	// to determine what the customer pays.
	dAmount := decimal.NewFromInt(req.Amount)
	if operation_setting.GetQuotaDisplayType() == operation_setting.QuotaDisplayTypeTokens {
		dAmount = dAmount.Div(decimal.NewFromFloat(common.QuotaPerUnit))
	}
	discount := operation_setting.GetActiveAmountDiscount(int(req.Amount), time.Now())
	payMoney := dAmount.Mul(decimal.NewFromFloat(discount)).Mul(decimal.NewFromFloat(firstTopupPromoFactor(userID, req.Amount))).InexactFloat64()
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
	if invoice.OrderID != tradeNo || !strings.EqualFold(invoice.PriceCurrency, "usd") || decimal.NewFromFloat(float64(invoice.PriceAmount)).Sub(decimal.NewFromFloat(payMoney)).Abs().GreaterThan(decimal.NewFromFloat(0.005)) {
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
	return settleNowPaymentsPaymentWithContext(context.Background(), local, paymentID, callerIP)
}

func settleNowPaymentsPaymentWithContext(ctx context.Context, local *model.NowPaymentsPayment, paymentID, callerIP string) error {
	if local == nil || strings.TrimSpace(paymentID) == "" {
		return fmt.Errorf("%w: payment not found", errNowPaymentsVerification)
	}
	remote, err := getNowPaymentsPayment(ctx, paymentID)
	if err != nil {
		return err
	}
	if remote == nil {
		return fmt.Errorf("%w: empty provider response", errNowPaymentsVerification)
	}
	return settleNowPaymentsPaymentResponse(local, remote, paymentID, callerIP)
}

func settleNowPaymentsPaymentResponse(local *model.NowPaymentsPayment, remote *service.NowPaymentsPaymentResponse, paymentID, callerIP string) error {
	if local == nil || remote == nil || strings.TrimSpace(paymentID) == "" {
		return fmt.Errorf("%w: payment not found", errNowPaymentsVerification)
	}
	remotePaymentID := strings.TrimSpace(string(remote.PaymentID))
	remoteInvoiceID := strings.TrimSpace(string(remote.InvoiceID))
	remoteOrderID := strings.TrimSpace(remote.OrderID)
	parentPaymentID := strings.TrimSpace(string(remote.ParentPaymentID))
	localPaymentID := strings.TrimSpace(local.PaymentID)
	if remotePaymentID != strings.TrimSpace(paymentID) || !strings.EqualFold(remote.PriceCurrency, "usd") || (remoteOrderID != local.TopUpTradeNo && remotePaymentID != localPaymentID && (remoteOrderID != "" || parentPaymentID == "" || parentPaymentID != localPaymentID)) {
		return fmt.Errorf("%w: payment details mismatch", errNowPaymentsVerification)
	}
	if local.InvoiceID != "" && remoteInvoiceID != local.InvoiceID {
		return fmt.Errorf("%w: invoice id mismatch", errNowPaymentsVerification)
	}
	if local.InvoiceID == "" && (remoteInvoiceID != "" || remote.PayAddress != local.PayAddress || !strings.EqualFold(remote.PayCurrency, local.PayCurrency)) {
		return fmt.Errorf("%w: legacy payment details mismatch", errNowPaymentsVerification)
	}
	if local.InvoiceID == "" && local.PaymentID != remotePaymentID {
		return fmt.Errorf("%w: payment id mismatch", errNowPaymentsVerification)
	}
	topUp := model.GetTopUpByTradeNo(local.TopUpTradeNo)
	if topUp == nil || topUp.PaymentProvider != model.PaymentProviderNowPayments {
		return fmt.Errorf("%w: topup not found", errNowPaymentsVerification)
	}
	if decimal.NewFromFloat(float64(remote.PriceAmount)).Sub(decimal.NewFromFloat(topUp.Money)).Abs().GreaterThan(decimal.NewFromFloat(0.005)) {
		return fmt.Errorf("%w: payment amount mismatch", errNowPaymentsVerification)
	}
	legacyPayAmount := local.PayAmount
	remoteStatus := strings.ToLower(strings.TrimSpace(remote.PaymentStatus))
	if remoteStatus != "finished" && remoteStatus != "partially_paid" {
		return recordNowPaymentsAttempt(local, remote, remotePaymentID, callerIP, false)
	}
	expectedCryptoAmount := decimal.NewFromFloat(float64(remote.PayAmount))
	if local.InvoiceID == "" {
		legacyAmount, err := decimal.NewFromString(legacyPayAmount)
		if err != nil || expectedCryptoAmount.Sub(legacyAmount).Abs().GreaterThan(decimal.NewFromFloat(0.00000001)) {
			return fmt.Errorf("%w: legacy cryptocurrency amount mismatch", errNowPaymentsVerification)
		}
	}
	if !nowPaymentsPaymentCoversAmount(float64(remote.PayAmount), float64(remote.ActuallyPaid)) {
		return recordNowPaymentsAttempt(local, remote, remotePaymentID, callerIP, false)
	}
	return recordNowPaymentsAttempt(local, remote, remotePaymentID, callerIP, true)
}

func recordNowPaymentsAttempt(local *model.NowPaymentsPayment, remote *service.NowPaymentsPaymentResponse, paymentID, callerIP string, successful bool) error {
	if local == nil || remote == nil {
		return fmt.Errorf("%w: payment not found", errNowPaymentsVerification)
	}
	var quota int
	var userID int
	var tradeNo string
	var duplicateAttemptID int
	var paidAmount float64
	var paymentMethod string
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		locked := &model.TopUp{}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("trade_no = ?", local.TopUpTradeNo).First(locked).Error; err != nil {
			return err
		}
		if locked.PaymentProvider != model.PaymentProviderNowPayments || decimal.NewFromFloat(float64(remote.PriceAmount)).Sub(decimal.NewFromFloat(locked.Money)).Abs().GreaterThan(decimal.NewFromFloat(0.005)) {
			return fmt.Errorf("%w: order changed during verification", errNowPaymentsVerification)
		}
		currentPayment := &model.NowPaymentsPayment{}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", local.Id).First(currentPayment).Error; err != nil {
			return err
		}
		if currentPayment.TopUpTradeNo != locked.TradeNo || (currentPayment.InvoiceID != "" && strings.TrimSpace(string(remote.InvoiceID)) != currentPayment.InvoiceID) {
			return fmt.Errorf("%w: invoice changed during verification", errNowPaymentsVerification)
		}
		failedOrderCanRetry := locked.Status == common.TopUpStatusFailed && currentPayment.InvoiceID != "" && currentPayment.ProviderPayload == "verified" && (currentPayment.PaymentStatus == "failed" || currentPayment.PaymentStatus == "expired" || currentPayment.PaymentStatus == "refunded")
		parentPaymentID := strings.TrimSpace(string(remote.ParentPaymentID))
		// A linked payment can be the first successful payment (for example,
		// when the payer changes currency). It is only a duplicate after crediting.
		// Keep accepting its callbacks after it becomes the bound payment, and
		// retain the original parent binding through verified attempt history.
		if parentPaymentID != "" && currentPayment.PaymentID != parentPaymentID && currentPayment.PaymentID != paymentID {
			var parentAttempt model.NowPaymentsAttempt
			parentLookup := tx.Where("payment_id = ? AND top_up_trade_no = ? AND invoice_id = ?", parentPaymentID, locked.TradeNo, currentPayment.InvoiceID).Limit(1).Find(&parentAttempt)
			if parentLookup.Error != nil {
				return parentLookup.Error
			}
			if parentLookup.RowsAffected == 0 {
				return fmt.Errorf("%w: repeated payment parent mismatch", errNowPaymentsVerification)
			}
		}
		attempt := &model.NowPaymentsAttempt{}
		lookup := tx.Where("payment_id = ?", paymentID).Limit(1).Find(attempt)
		if lookup.Error != nil {
			return lookup.Error
		}
		if lookup.RowsAffected == 0 {
			attempt = &model.NowPaymentsAttempt{PaymentID: paymentID}
		}
		if attempt.Id != 0 && attempt.TopUpTradeNo != locked.TradeNo {
			return fmt.Errorf("%w: payment belongs to another order", errNowPaymentsVerification)
		}
		attempt.TopUpTradeNo = locked.TradeNo
		attempt.InvoiceID = currentPayment.InvoiceID
		attempt.PayAddress = remote.PayAddress
		attempt.PayinExtraID = remote.PayinExtraID
		attempt.PayCurrency = remote.PayCurrency
		attempt.PayAmount = strconv.FormatFloat(float64(remote.PayAmount), 'f', -1, 64)
		attempt.ActuallyPaid = strconv.FormatFloat(float64(remote.ActuallyPaid), 'f', -1, 64)
		attempt.Network = remote.Network
		attempt.PaymentStatus = strings.ToLower(strings.TrimSpace(remote.PaymentStatus))
		attempt.Underpaid = !successful && (attempt.PaymentStatus == "finished" || attempt.PaymentStatus == "partially_paid")
		attempt.PayinHash = remote.PayinHash
		attempt.Normalize()
		if err := tx.Save(attempt).Error; err != nil {
			return err
		}
		if locked.Status != common.TopUpStatusSuccess || currentPayment.PaymentID == paymentID {
			currentPayment.PaymentID = paymentID
			currentPayment.PayAddress = remote.PayAddress
			currentPayment.PayinExtraID = remote.PayinExtraID
			currentPayment.PayCurrency = remote.PayCurrency
			currentPayment.PayAmount = attempt.PayAmount
			currentPayment.ActuallyPaid = attempt.ActuallyPaid
			currentPayment.Network = remote.Network
			currentPayment.PaymentStatus = attempt.PaymentStatus
			currentPayment.PayinHash = remote.PayinHash
			currentPayment.ProviderPayload = "verified"
			currentPayment.ExpiresAt = service.ParseNowPaymentsExpiry(remote.ExpirationEstimateDate)
			if err := tx.Save(currentPayment).Error; err != nil {
				return err
			}
		}

		if successful && locked.Status == common.TopUpStatusSuccess {
			if currentPayment.PaymentID != paymentID {
				if !attempt.DuplicatePayment {
					attempt.DuplicatePayment = true
					if err := tx.Save(attempt).Error; err != nil {
						return err
					}
				}
				duplicateAttemptID = attempt.Id
			}
			return nil
		}
		if successful {
			if locked.Status != common.TopUpStatusPending && locked.Status != common.TopUpStatusFailed {
				return fmt.Errorf("topup status invalid")
			}
			if locked.Status == common.TopUpStatusFailed && !failedOrderCanRetry {
				return fmt.Errorf("%w: failed order requires manual review", errNowPaymentsVerification)
			}
			quota = int(decimal.NewFromInt(locked.Amount).Mul(decimal.NewFromFloat(common.QuotaPerUnit)).IntPart())
			if quota <= 0 {
				return fmt.Errorf("invalid quota")
			}
			model.MarkTopUpSuccess(locked)
			if err := tx.Save(locked).Error; err != nil {
				return err
			}
		}
		if quota > 0 {
			userID = locked.UserId
			tradeNo = locked.TradeNo
			paidAmount = locked.Money
			paymentMethod = locked.PaymentMethod
			return tx.Model(&model.User{}).Where("id = ?", locked.UserId).Update("quota", gorm.Expr("quota + ?", quota)).Error
		}
		return nil
	})
	if err != nil {
		return err
	}
	if quota > 0 {
		model.RecordTopupLog(userID, fmt.Sprintf("NOWPayments top-up successful, credited amount: %v, amount paid: %.2f", logger.FormatQuota(quota), paidAmount), callerIP, paymentMethod, model.PaymentMethodNowPayments)
		onNowPaymentsTopupSucceeded(userID, quota, model.PaymentMethodNowPayments, tradeNo)
	}
	if duplicateAttemptID != 0 {
		return deliverNowPaymentsAlert(duplicateAttemptID)
	}
	return nil
}

func deliverNowPaymentsAlert(attemptID int) error {
	leaseUntil := common.GetTimestamp() + 120
	claimed, err := model.ClaimNowPaymentsAlert(attemptID, leaseUntil)
	if err != nil || !claimed {
		return err
	}
	attempt := model.GetNowPaymentsAttemptByIDFromPK(attemptID)
	if attempt == nil {
		return model.CompleteNowPaymentsAlert(attemptID, leaseUntil, false)
	}
	local := model.GetNowPaymentsPaymentByTradeNo(attempt.TopUpTradeNo)
	if local == nil || common.FeishuOpsChatID() == "" || common.FeishuAppID() == "" || common.FeishuAppSecret() == "" {
		_ = model.CompleteNowPaymentsAlert(attemptID, leaseUntil, false)
		return fmt.Errorf("NOWPayments duplicate payment alert is not configured or order is missing: %s", attempt.PaymentID)
	}
	err = notifyDuplicateNowPaymentsPayment(local, local.PaymentID, attempt)
	if completeErr := model.CompleteNowPaymentsAlert(attemptID, leaseUntil, err == nil); completeErr != nil {
		return completeErr
	}
	return err
}

func notifyDuplicateNowPaymentsPayment(local *model.NowPaymentsPayment, winningPaymentID string, attempt *model.NowPaymentsAttempt) error {
	if local == nil || attempt == nil {
		return nil
	}
	lines := []string{
		"内部订单号：" + local.TopUpTradeNo,
		"Invoice ID：" + local.InvoiceID,
		"已入账 Payment ID：" + winningPaymentID,
		"重复成功 Payment ID：" + attempt.PaymentID,
		"重复付款币种：" + strings.ToUpper(attempt.PayCurrency),
		"重复付款金额：" + attempt.ActuallyPaid,
		"重复付款状态：" + attempt.PaymentStatus,
	}
	if attempt.Network != "" {
		lines = append(lines, "网络："+attempt.Network)
	}
	if attempt.PayinHash != "" {
		lines = append(lines, "交易哈希："+attempt.PayinHash)
	}
	return sendNowPaymentsDuplicateAlert(common.FeishuOpsChatID(), common.FeishuNotificationTitle("NOWPayments 同一订单重复付款告警"), lines)
}

func NowPaymentsWebhook(c *gin.Context) {
	body, err := io.ReadAll(c.Request.Body)
	if err != nil || !service.VerifyNowPaymentsIPN(body, c.GetHeader("x-nowpayments-sig")) {
		c.Status(http.StatusUnauthorized)
		return
	}
	var payload struct {
		PaymentID       dto.StringValue `json:"payment_id"`
		ParentPaymentID dto.StringValue `json:"parent_payment_id"`
		OrderID         string          `json:"order_id"`
	}
	if err := common.Unmarshal(body, &payload); err != nil || strings.TrimSpace(string(payload.PaymentID)) == "" {
		c.Status(http.StatusBadRequest)
		return
	}
	var local *model.NowPaymentsPayment
	if strings.TrimSpace(payload.OrderID) != "" {
		local = model.GetNowPaymentsPaymentByTradeNo(payload.OrderID)
	} else if strings.TrimSpace(string(payload.ParentPaymentID)) != "" {
		local = model.GetNowPaymentsPaymentByID(string(payload.ParentPaymentID))
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
