package controller

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
	"github.com/thanhpk/randstr"
)

type ClinkPayRequest struct {
	Amount        int64  `json:"amount"`
	PaymentMethod string `json:"payment_method"`
	PlanId        int    `json:"plan_id,omitempty"`
	SuccessURL    string `json:"success_url,omitempty"`
	CancelURL     string `json:"cancel_url,omitempty"`
}

type ClinkConfirmRequest struct {
	SessionID string `json:"session_id"`
}

func getClinkMinTopup(userId int) int64 {
	return getWalletMinTopupForUser(userId, setting.ClinkMinTopUp)
}

func getClinkSuccessURL(custom string) string {
	if strings.TrimSpace(custom) != "" {
		return custom
	}
	if strings.TrimSpace(setting.ClinkSuccessURL) != "" {
		return setting.ClinkSuccessURL
	}
	return strings.TrimRight(system_setting.ServerAddress, "/") + "/console/wallet?show_history=true"
}

func getClinkCancelURL(custom string) string {
	if strings.TrimSpace(custom) != "" {
		return custom
	}
	if strings.TrimSpace(setting.ClinkCancelURL) != "" {
		return setting.ClinkCancelURL
	}
	return strings.TrimRight(system_setting.ServerAddress, "/") + "/console/wallet"
}

func RequestClinkPay(c *gin.Context) {
	if abortIfTopupForbidden(c) {
		return
	}
	if !isClinkTopUpEnabled() {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": i18n.T(c, i18n.MsgPaymentNotConfigured)})
		return
	}

	var req ClinkPayRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": i18n.T(c, i18n.MsgInvalidParams)})
		return
	}
	if req.PaymentMethod != "" && req.PaymentMethod != model.PaymentMethodClink {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": i18n.T(c, i18n.MsgPaymentMethodNotExists)})
		return
	}
	id := c.GetInt("id")
	if req.PlanId <= 0 {
		minTopup := getClinkMinTopup(id)
		if req.Amount < minTopup {
			c.JSON(http.StatusOK, gin.H{"message": "error", "data": i18n.T(c, i18n.MsgTopupAmountMin, map[string]any{"Min": minTopup})})
			return
		}
		if req.Amount > 10000 {
			c.JSON(http.StatusOK, gin.H{"message": "error", "data": i18n.T(c, i18n.MsgTopupAmountMax, map[string]any{"Max": 10000})})
			return
		}
	}
	if req.SuccessURL != "" && common.ValidateRedirectURL(req.SuccessURL) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": i18n.T(c, i18n.MsgPaymentRedirectUntrusted), "data": ""})
		return
	}
	if req.CancelURL != "" && common.ValidateRedirectURL(req.CancelURL) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": i18n.T(c, i18n.MsgPaymentRedirectUntrusted), "data": ""})
		return
	}

	TouchUserCountry(id, c.ClientIP())
	user, err := model.GetUserById(id, false)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": i18n.T(c, i18n.MsgOAuthGetUserErr)})
		return
	}

	var plan *model.SubscriptionPlan
	var terms subscriptionOrderTerms
	chargedMoney := 0.0
	if req.PlanId > 0 {
		plan, terms, err = resolvePaidSubscriptionPayment(id, req.PlanId)
		if err != nil {
			common.ApiErrorMsg(c, err.Error())
			return
		}
		if terms.Payable+0.005 < float64(setting.ClinkMinTopUp) {
			common.ApiErrorMsg(c, fmt.Sprintf("Clink minimum payment amount is $%d", setting.ClinkMinTopUp))
			return
		}
		chargedMoney = terms.Payable
	} else {
		chargedMoney = GetChargedAmountWithTierDiscount(req.Amount, *user) * firstTopupPromoFactor(id, req.Amount)
	}
	if chargedMoney <= 0.01 {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": i18n.T(c, i18n.MsgTopupAmountTooLow)})
		return
	}

	tradePrefix := "CLINK"
	if plan != nil {
		tradePrefix = "SUB-CLINK"
	}
	tradeNo := fmt.Sprintf("%s-%d-%d-%s", tradePrefix, id, time.Now().UnixMilli(), randstr.String(6))
	if plan != nil {
		order := newPaidSubscriptionOrder(id, plan, terms, tradeNo, model.PaymentMethodClink, model.PaymentProviderClink)
		if err := order.Insert(); err != nil {
			logger.LogError(c.Request.Context(), fmt.Sprintf("Clink 创建订阅订单失败 user_id=%d trade_no=%s plan_id=%d error=%q", id, tradeNo, plan.Id, err.Error()))
			c.JSON(http.StatusOK, gin.H{"message": "error", "data": i18n.T(c, i18n.MsgPaymentCreateFailed)})
			return
		}
	} else {
		amount := req.Amount
		if operation_setting.GetQuotaDisplayType() == operation_setting.QuotaDisplayTypeTokens {
			amount = amount / int64(common.QuotaPerUnit)
		}
		topUp := &model.TopUp{
			UserId:              id,
			Amount:              amount,
			PaidAmountUSD:       chargedMoney,
			PaidAmountUSDSource: "order",
			Money:               chargedMoney,
			TradeNo:             tradeNo,
			PaymentMethod:       model.PaymentMethodClink,
			PaymentProvider:     model.PaymentProviderClink,
			CreateTime:          common.GetTimestamp(),
			Status:              common.TopUpStatusPending,
		}
		if err := topUp.FillCountryFromIP(c.ClientIP(), user.Country).Insert(); err != nil {
			logger.LogError(c.Request.Context(), fmt.Sprintf("Clink 创建本地订单失败 user_id=%d trade_no=%s error=%q", id, tradeNo, err.Error()))
			c.JSON(http.StatusOK, gin.H{"message": "error", "data": i18n.T(c, i18n.MsgPaymentCreateFailed)})
			return
		}
	}

	currency := strings.TrimSpace(setting.ClinkCurrency)
	if currency == "" {
		currency = "USD"
	}

	session, err := service.CreateClinkCheckoutSession(c.Request.Context(), &service.ClinkCheckoutCreateRequest{
		CustomerEmail:       user.Email,
		OriginalAmount:      chargedMoney,
		OriginalCurrency:    currency,
		MerchantReferenceID: tradeNo,
		UIMode:              "hostedPage",
		SuccessURL: func() string {
			if plan != nil {
				return subscriptionPaymentURL(plan, "success")
			}
			return getClinkSuccessURL(req.SuccessURL)
		}(),
		CancelURL: func() string {
			if plan != nil {
				return subscriptionPaymentURL(plan, "cancelled")
			}
			return getClinkCancelURL(req.CancelURL)
		}(),
		Metadata: map[string]string{
			"trade_no": tradeNo,
			"user_id":  strconv.Itoa(user.Id),
			"purpose": func() string {
				if plan != nil {
					return model.NormalizeSubscriptionPlanType(plan.PlanType)
				}
				return "wallet_topup"
			}(),
		},
	})
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Clink 创建 Checkout Session 失败 user_id=%d trade_no=%s error=%q", id, tradeNo, err.Error()))
		if plan != nil {
			_, _ = tryExpireSubscriptionPayment(tradeNo, model.PaymentProviderClink)
		} else {
			_ = model.UpdatePendingTopUpStatus(tradeNo, model.PaymentProviderClink, common.TopUpStatusFailed)
		}
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": i18n.T(c, i18n.MsgPaymentStartFailed)})
		return
	}

	logger.LogInfo(c.Request.Context(), fmt.Sprintf("Clink 充值订单创建成功 user_id=%d trade_no=%s session_id=%s amount=%.2f %s", id, tradeNo, session.SessionID, chargedMoney, currency))
	c.JSON(http.StatusOK, gin.H{
		"message": "success",
		"data": gin.H{
			"checkout_url": session.URL,
			"session_id":   session.SessionID,
			"order_id":     tradeNo,
		},
	})
}

func ConfirmClinkPay(c *gin.Context) {
	if !isClinkTopUpEnabled() {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": i18n.T(c, i18n.MsgPaymentNotConfigured)})
		return
	}

	var req ClinkConfirmRequest
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.SessionID) == "" {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": i18n.T(c, i18n.MsgInvalidParams)})
		return
	}

	userID := c.GetInt("id")
	if userID <= 0 {
		c.AbortWithStatus(http.StatusUnauthorized)
		return
	}
	tradeNo, err := confirmClinkSession(c, req.SessionID, "", userID)
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Clink confirm 入账失败 user_id=%d session_id=%s error=%q", userID, req.SessionID, err.Error()))
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": i18n.T(c, i18n.MsgPaymentStartFailed)})
		return
	}

	logger.LogInfo(c.Request.Context(), fmt.Sprintf("Clink confirm 入账成功 user_id=%d trade_no=%s session_id=%s", userID, tradeNo, req.SessionID))
	c.JSON(http.StatusOK, gin.H{
		"message": "success",
		"data": gin.H{
			"order_id": tradeNo,
			"status":   "success",
		},
	})
}

func ClinkWebhook(c *gin.Context) {
	if !isClinkWebhookEnabled() {
		logger.LogWarn(c.Request.Context(), fmt.Sprintf("Clink webhook rejected reason=disabled client_ip=%s", c.ClientIP()))
		c.String(http.StatusForbidden, "webhook disabled")
		return
	}

	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Clink webhook read body failed client_ip=%s error=%q", c.ClientIP(), err.Error()))
		c.String(http.StatusBadRequest, "bad request")
		return
	}

	timestamp := c.GetHeader("X-Clink-Timestamp")
	signature := c.GetHeader("X-Clink-Signature")
	if !service.VerifyClinkWebhookSignature(timestamp, signature, string(bodyBytes)) {
		logger.LogWarn(c.Request.Context(), fmt.Sprintf("Clink webhook signature invalid client_ip=%s timestamp=%q", c.ClientIP(), timestamp))
		c.String(http.StatusUnauthorized, "invalid signature")
		return
	}

	var eventMeta struct {
		ID   string `json:"id"`
		Type string `json:"type"`
	}
	_ = common.Unmarshal(bodyBytes, &eventMeta)
	logger.LogInfo(c.Request.Context(), fmt.Sprintf("Clink webhook received event_id=%s event_type=%s client_ip=%s payload_bytes=%d", eventMeta.ID, eventMeta.Type, c.ClientIP(), len(bodyBytes)))

	if err := handleClinkWebhook(c, bodyBytes); err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Clink webhook handling failed event_id=%s event_type=%s client_ip=%s error=%q", eventMeta.ID, eventMeta.Type, c.ClientIP(), err.Error()))
		c.String(http.StatusInternalServerError, "webhook processing failed")
		return
	}
	c.String(http.StatusOK, "OK")
}

func handleClinkWebhook(c *gin.Context, bodyBytes []byte) error {
	var event service.ClinkWebhookEvent
	if err := common.Unmarshal(bodyBytes, &event); err != nil {
		return fmt.Errorf("invalid clink webhook json: %w", err)
	}

	switch event.Type {
	case "order.succeeded":
		var order service.ClinkOrderWebhookData
		if err := service.DecodeClinkWebhookData(event.Data, &order); err != nil {
			return fmt.Errorf("invalid clink order payload: %w", err)
		}
		if strings.ToLower(strings.TrimSpace(order.Status)) != "success" {
			return nil
		}
		if err := validateClinkCurrency(order.OriginalCurrency); err != nil {
			return err
		}
		paidAmount := service.ClinkAmountForValidation(order.AmountSubtotal, order.AmountTotal, order.OriginalCurrency, order.PaymentCurrency)
		return completeClinkTopUp(c, order.MerchantReferenceID, paidAmount)
	case "order.failed":
		var order service.ClinkOrderWebhookData
		if err := service.DecodeClinkWebhookData(event.Data, &order); err != nil {
			return fmt.Errorf("invalid clink order payload: %w", err)
		}
		// A checkout can contain several payment attempts. A failed attempt
		// must not terminate the merchant order or downgrade a successful one.
		logger.LogInfo(c.Request.Context(), fmt.Sprintf("Clink payment attempt failed; merchant order unchanged trade_no=%s order_id=%s", order.MerchantReferenceID, order.OrderID))
		return nil
	case "session.complete":
		var session service.ClinkSessionWebhookData
		if err := service.DecodeClinkWebhookData(event.Data, &session); err != nil {
			return fmt.Errorf("invalid clink session payload: %w", err)
		}
		if strings.ToLower(strings.TrimSpace(session.PaymentStatus)) != "paid" {
			return nil
		}
		if strings.TrimSpace(session.MerchantReferenceID) == "" {
			return fmt.Errorf("missing merchantReferenceId")
		}
		_, err := confirmClinkSession(c, session.SessionID, session.MerchantReferenceID, 0)
		return err
	case "refund.succeeded":
		var refund service.ClinkRefundWebhookData
		if err := service.DecodeClinkWebhookData(event.Data, &refund); err != nil {
			return fmt.Errorf("invalid clink refund payload: %w", err)
		}
		return handleClinkRefund(c, &refund)
	case "dispute.lost", "dispute.won", "dispute.created", "dispute.updated", "dispute.closed":
		// Chargebacks carry only Clink's internal orderId (no merchantReferenceId),
		// so we cannot auto-link to a top_up yet. Log loudly for manual clawback.
		logger.LogError(c.Request.Context(), fmt.Sprintf("Clink 拒付事件需人工处理 event_type=%s data=%s client_ip=%s", event.Type, string(event.Data), c.ClientIP()))
		return nil
	default:
		logger.LogInfo(c.Request.Context(), fmt.Sprintf("Clink webhook ignored event_type=%s", event.Type))
		return nil
	}
}

// handleClinkRefund reverses a top-up when Clink reports a completed refund.
// Only full refunds are auto-processed; partial refunds are logged for manual
// review to avoid over-clawing on cumulative partial refunds.
func handleClinkRefund(c *gin.Context, refund *service.ClinkRefundWebhookData) error {
	tradeNo := strings.TrimSpace(refund.Metadata.MerchantReferenceID)
	if tradeNo == "" {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Clink 退款缺少 merchantReferenceId 无法关联订单 refund_id=%s clink_order_id=%s client_ip=%s", refund.RefundID, refund.OrderID, c.ClientIP()))
		return nil
	}

	if order := model.GetSubscriptionOrderByTradeNo(tradeNo); order != nil {
		if refund.RefundAmount+0.001 < order.Money {
			logger.LogError(c.Request.Context(), fmt.Sprintf("Clink 订阅部分退款需人工处理 trade_no=%s refund=$%.2f order=$%.2f refund_id=%s client_ip=%s",
				tradeNo, refund.RefundAmount, order.Money, refund.RefundID, c.ClientIP()))
			return nil
		}
		LockOrder(tradeNo)
		defer UnlockOrder(tradeNo)
		_, err := tryReverseSubscriptionPayment(tradeNo, refund.RefundAmount, "refund", common.GetJsonString(refund))
		if err != nil {
			logger.LogError(c.Request.Context(), fmt.Sprintf("Clink 订阅退款处理失败 trade_no=%s refund_id=%s client_ip=%s error=%q", tradeNo, refund.RefundID, c.ClientIP(), err.Error()))
		}
		return err
	}

	topUp := model.GetTopUpByTradeNo(tradeNo)
	if topUp == nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Clink 退款订单不存在 trade_no=%s refund_id=%s client_ip=%s", tradeNo, refund.RefundID, c.ClientIP()))
		return nil
	}
	// Partial-refund guard: only full refunds reverse automatically.
	if refund.RefundAmount+0.001 < topUp.Money {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Clink 部分退款需人工处理 trade_no=%s refund=$%.2f order=$%.2f refund_id=%s client_ip=%s",
			tradeNo, refund.RefundAmount, topUp.Money, refund.RefundID, c.ClientIP()))
		return nil
	}

	LockOrder(tradeNo)
	defer UnlockOrder(tradeNo)
	reversed, userID, err := model.RefundClinkTopUp(tradeNo, refund.RefundAmount, refund.RefundID)
	if err != nil {
		if errors.Is(err, model.ErrTopUpStatusInvalid) {
			logger.LogInfo(c.Request.Context(), fmt.Sprintf("Clink 退款幂等跳过（订单非 success）trade_no=%s refund_id=%s", tradeNo, refund.RefundID))
			return nil
		}
		logger.LogError(c.Request.Context(), fmt.Sprintf("Clink 退款处理失败 trade_no=%s refund_id=%s client_ip=%s error=%q", tradeNo, refund.RefundID, c.ClientIP(), err.Error()))
		return err
	}
	logger.LogInfo(c.Request.Context(), fmt.Sprintf("Clink 退款回收成功 trade_no=%s user_id=%d refund=$%.2f 回收额度=%d refund_id=%s client_ip=%s",
		tradeNo, userID, refund.RefundAmount, reversed, refund.RefundID, c.ClientIP()))
	return nil
}

func completeClinkTopUp(c *gin.Context, tradeNo string, paidAmount float64) error {
	tradeNo = strings.TrimSpace(tradeNo)
	if tradeNo == "" {
		return fmt.Errorf("missing merchantReferenceId")
	}

	expectedMoney := 0.0
	if order := model.GetSubscriptionOrderByTradeNo(tradeNo); order != nil {
		expectedMoney = order.Money
	} else if topUp := model.GetTopUpByTradeNo(tradeNo); topUp != nil {
		expectedMoney = topUp.Money
	} else {
		return fmt.Errorf("payment order not found trade_no=%s", tradeNo)
	}
	if !service.ClinkAmountsMatch(expectedMoney, paidAmount) {
		return fmt.Errorf("amount mismatch expected=%.2f actual=%.2f trade_no=%s", expectedMoney, paidAmount, tradeNo)
	}

	LockOrder(tradeNo)
	defer UnlockOrder(tradeNo)
	handled, err := tryCompleteSubscriptionPayment(tradeNo, "", model.PaymentProviderClink, model.PaymentMethodClink)
	if handled || err != nil {
		return err
	}
	return model.RechargeClink(tradeNo, c.ClientIP())
}

// All recovery paths query Clink themselves; client/webhook fields alone cannot
// revive a failed order. userID=0 is reserved for signed callbacks and admins.
var getClinkSessionForConfirmation = service.GetClinkCheckoutSession

func validateClinkCurrency(currency string) error {
	expected := strings.TrimSpace(setting.ClinkCurrency)
	if expected == "" {
		expected = "USD"
	}
	if !strings.EqualFold(strings.TrimSpace(currency), expected) {
		return fmt.Errorf("clink original currency mismatch")
	}
	return nil
}

func confirmClinkSession(c *gin.Context, sessionID, expectedTradeNo string, userID int) (string, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return "", fmt.Errorf("missing clink session id")
	}
	session, err := getClinkSessionForConfirmation(c.Request.Context(), sessionID)
	if err != nil {
		return "", err
	}
	if session.SessionID != sessionID || !strings.EqualFold(strings.TrimSpace(session.PaymentStatus), "paid") {
		return "", fmt.Errorf("clink session is not paid or does not match")
	}
	tradeNo := strings.TrimSpace(session.MerchantReferenceID)
	if tradeNo == "" && session.Metadata != nil {
		tradeNo = strings.TrimSpace(session.Metadata["trade_no"])
	}
	if tradeNo == "" || (expectedTradeNo != "" && tradeNo != expectedTradeNo) {
		return "", fmt.Errorf("clink merchant reference mismatch")
	}
	if err := validateClinkCurrency(session.OriginalCurrency); err != nil {
		return tradeNo, err
	}
	paidAmount := service.ClinkAmountForValidation(session.AmountSubtotal, session.AmountTotal, session.OriginalCurrency, session.PaymentCurrency)
	LockOrder(tradeNo)
	defer UnlockOrder(tradeNo)
	if order := model.GetSubscriptionOrderByTradeNo(tradeNo); order != nil {
		if (userID > 0 && order.UserId != userID) || order.PaymentProvider != model.PaymentProviderClink || !service.ClinkAmountsMatch(order.Money, paidAmount) {
			return tradeNo, fmt.Errorf("clink subscription owner, provider or amount mismatch")
		}
		return tradeNo, model.CompleteSubscriptionOrder(tradeNo, common.GetJsonString(session), model.PaymentProviderClink, model.PaymentMethodClink)
	}
	topUp := model.GetTopUpByTradeNo(tradeNo)
	if topUp == nil || (userID > 0 && topUp.UserId != userID) || topUp.PaymentProvider != model.PaymentProviderClink || !service.ClinkAmountsMatch(topUp.Money, paidAmount) {
		return tradeNo, fmt.Errorf("clink topup owner, provider or amount mismatch")
	}
	return tradeNo, model.RechargeClinkVerified(tradeNo, c.ClientIP(), paidAmount)
}

// AdminReconcileClinkTopUp verifies the upstream session before attempting a
// repair. It uses the same idempotent settlement as user confirmation/webhooks.
func AdminReconcileClinkTopUp(c *gin.Context) {
	var req struct {
		TradeNo   string `json:"trade_no"`
		SessionID string `json:"session_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.TradeNo) == "" || strings.TrimSpace(req.SessionID) == "" {
		common.ApiErrorMsg(c, "trade_no and session_id are required")
		return
	}
	tradeNo, err := confirmClinkSession(c, req.SessionID, strings.TrimSpace(req.TradeNo), 0)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	logger.LogInfo(c.Request.Context(), fmt.Sprintf("Clink admin reconciliation succeeded admin_id=%d trade_no=%s session_id=%s", c.GetInt("id"), tradeNo, req.SessionID))
	common.ApiSuccess(c, gin.H{"trade_no": tradeNo, "status": "success"})
}
