package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
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

type PayPalPayRequest struct {
	Amount        int64  `json:"amount"`
	PaymentMethod string `json:"payment_method"`
	PlanId        int    `json:"plan_id,omitempty"`
	SuccessURL    string `json:"success_url,omitempty"`
	CancelURL     string `json:"cancel_url,omitempty"`
}

type payPalWebhookEvent struct {
	EventType string          `json:"event_type"`
	Resource  json.RawMessage `json:"resource"`
}

type payPalOrderResource struct {
	ID            string `json:"id"`
	PurchaseUnits []struct {
		ReferenceID string `json:"reference_id"`
		CustomID    string `json:"custom_id"`
	} `json:"purchase_units"`
}

type payPalCaptureResource struct {
	CustomID string `json:"custom_id"`
	Amount   struct {
		Value        string `json:"value"`
		CurrencyCode string `json:"currency_code"`
	} `json:"amount"`
}

// payPalRefundResource models the PAYMENT.CAPTURE.REFUNDED / REVERSED resource.
type payPalRefundResource struct {
	ID       string `json:"id"`
	Status   string `json:"status"`
	CustomID string `json:"custom_id"`
	Amount   struct {
		Value        string `json:"value"`
		CurrencyCode string `json:"currency_code"`
	} `json:"amount"`
}

// parsePayPalRefundUSD validates and parses a PayPal refund amount (USD string).
func parsePayPalRefundUSD(currency, value string) (float64, error) {
	if currency != "" && currency != "USD" {
		return 0, fmt.Errorf("unsupported refund currency %q", currency)
	}
	amt, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid refund amount %q: %w", value, err)
	}
	if amt <= 0 {
		return 0, fmt.Errorf("non-positive refund amount %q", value)
	}
	return amt, nil
}

func RequestPayPalAmount(c *gin.Context) {
	if abortIfTopupForbidden(c) {
		return
	}
	var req PayPalPayRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": i18n.T(c, i18n.MsgInvalidParams)})
		return
	}
	if req.Amount < getPayPalMinTopup() {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": i18n.T(c, i18n.MsgTopupAmountMin, map[string]any{"Min": getPayPalMinTopup()})})
		return
	}
	id := c.GetInt("id")
	user, err := model.GetUserById(id, false)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": i18n.T(c, i18n.MsgOAuthGetUserErr)})
		return
	}
	payMoney := GetChargedAmountWithTierDiscount(req.Amount, *user) * firstTopupPromoFactor(id, req.Amount)
	if payMoney <= 0.01 {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": i18n.T(c, i18n.MsgTopupAmountTooLow)})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "success", "data": strconv.FormatFloat(payMoney, 'f', 2, 64)})
}

func RequestPayPalPay(c *gin.Context) {
	if abortIfTopupForbidden(c) {
		return
	}
	if !isPayPalTopUpEnabled() {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "PayPal 支付未启用"})
		return
	}
	var req PayPalPayRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": i18n.T(c, i18n.MsgInvalidParams)})
		return
	}
	if req.PaymentMethod != "" && req.PaymentMethod != model.PaymentMethodPayPal {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": i18n.T(c, i18n.MsgPaymentMethodNotExists)})
		return
	}
	if req.PlanId <= 0 {
		if req.Amount < getPayPalMinTopup() {
			c.JSON(http.StatusOK, gin.H{"message": "error", "data": i18n.T(c, i18n.MsgTopupAmountMin, map[string]any{"Min": getPayPalMinTopup()})})
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

	id := c.GetInt("id")
	TouchUserCountry(id, c.ClientIP())
	user, err := model.GetUserById(id, false)
	if err != nil || user == nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": i18n.T(c, i18n.MsgUserNotExists)})
		return
	}

	var plan *model.SubscriptionPlan
	var terms subscriptionOrderTerms
	chargedMoney := 0.0
	if req.PlanId > 0 {
		plan, terms, err = resolveGPTSubscriptionPayment(id, req.PlanId)
		if err != nil {
			common.ApiErrorMsg(c, err.Error())
			return
		}
		if terms.Payable+0.005 < float64(setting.PayPalMinTopUp) {
			common.ApiErrorMsg(c, fmt.Sprintf("PayPal 最低支付金额为 $%d", setting.PayPalMinTopUp))
			return
		}
		chargedMoney = terms.Payable
	} else {
		chargedMoney = GetChargedAmountWithTierDiscount(req.Amount, *user) * firstTopupPromoFactor(id, req.Amount)
	}

	referencePrefix := "new-api-paypal"
	if plan != nil {
		referencePrefix = "subscription-paypal"
	}
	reference := fmt.Sprintf("%s-%d-%d-%s", referencePrefix, user.Id, time.Now().UnixMilli(), randstr.String(4))
	referenceID := "pp_" + common.Sha1([]byte(reference))

	successURL := req.SuccessURL
	if successURL == "" {
		if plan != nil {
			successURL = freeModelPaymentURL("success")
		} else {
			successURL = system_setting.ServerAddress + "/console/usage-logs"
		}
	}
	cancelURL := req.CancelURL
	if cancelURL == "" {
		if plan != nil {
			cancelURL = freeModelPaymentURL("cancelled")
		} else {
			cancelURL = system_setting.ServerAddress + "/console/topup"
		}
	}

	if plan != nil {
		order := newGPTSubscriptionOrder(id, plan, terms, referenceID, model.PaymentMethodPayPal, model.PaymentProviderPayPal)
		if err := order.Insert(); err != nil {
			logger.LogError(c.Request.Context(), fmt.Sprintf("PayPal 创建订阅订单失败 user_id=%d trade_no=%s plan_id=%d error=%q", id, referenceID, plan.Id, err.Error()))
			c.JSON(http.StatusOK, gin.H{"message": "error", "data": i18n.T(c, i18n.MsgPaymentCreateFailed)})
			return
		}
	}

	approveURL, orderID, err := service.CreatePayPalOrder(referenceID, chargedMoney, successURL, cancelURL)
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("PayPal 创建订单失败 user_id=%d trade_no=%s amount=%d error=%q", id, referenceID, req.Amount, err.Error()))
		if plan != nil {
			_, _ = tryExpireSubscriptionPayment(referenceID, model.PaymentProviderPayPal)
		}
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": i18n.T(c, i18n.MsgPaymentStartFailed)})
		return
	}

	if plan == nil {
		amount := req.Amount
		if operation_setting.GetQuotaDisplayType() == operation_setting.QuotaDisplayTypeTokens {
			amount = amount / int64(common.QuotaPerUnit)
		}
		topUp := &model.TopUp{
			UserId:          id,
			Amount:          amount,
			Money:           chargedMoney,
			TradeNo:         referenceID,
			PaymentMethod:   model.PaymentMethodPayPal,
			PaymentProvider: model.PaymentProviderPayPal,
			CreateTime:      time.Now().Unix(),
			Status:          common.TopUpStatusPending,
		}
		if err := topUp.FillCountryFromIP(c.ClientIP(), user.Country).Insert(); err != nil {
			logger.LogError(c.Request.Context(), fmt.Sprintf("PayPal 创建充值订单失败 user_id=%d trade_no=%s amount=%d error=%q", id, referenceID, req.Amount, err.Error()))
			c.JSON(http.StatusOK, gin.H{"message": "error", "data": i18n.T(c, i18n.MsgPaymentCreateFailed)})
			return
		}
	}

	logger.LogInfo(c.Request.Context(), fmt.Sprintf("PayPal 支付订单创建成功 user_id=%d trade_no=%s order_id=%s plan_id=%d amount=%d money=%.2f", id, referenceID, orderID, req.PlanId, req.Amount, chargedMoney))
	c.JSON(http.StatusOK, gin.H{
		"message": "success",
		"data": gin.H{
			"pay_link": approveURL,
		},
	})
}

func PayPalWebhook(c *gin.Context) {
	ctx := c.Request.Context()
	if !isPayPalWebhookEnabled() {
		logger.LogWarn(ctx, fmt.Sprintf("PayPal webhook 被拒绝 reason=webhook_disabled path=%q client_ip=%s", c.Request.RequestURI, c.ClientIP()))
		c.AbortWithStatus(http.StatusForbidden)
		return
	}

	payload, err := io.ReadAll(c.Request.Body)
	if err != nil {
		logger.LogError(ctx, fmt.Sprintf("PayPal webhook 读取请求体失败 path=%q client_ip=%s error=%q", c.Request.RequestURI, c.ClientIP(), err.Error()))
		c.AbortWithStatus(http.StatusServiceUnavailable)
		return
	}

	if err := service.VerifyPayPalWebhook(c.Request.Header, payload); err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("PayPal webhook 验签失败 path=%q client_ip=%s error=%q", c.Request.RequestURI, c.ClientIP(), err.Error()))
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}

	var event payPalWebhookEvent
	if err := json.Unmarshal(payload, &event); err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("PayPal webhook 解析失败 path=%q client_ip=%s error=%q", c.Request.RequestURI, c.ClientIP(), err.Error()))
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}

	logger.LogInfo(ctx, fmt.Sprintf("PayPal webhook 验签成功 event_type=%s client_ip=%s", event.EventType, c.ClientIP()))
	switch event.EventType {
	case "CHECKOUT.ORDER.APPROVED":
		handlePayPalOrderApproved(ctx, event.Resource, c.ClientIP())
	case "PAYMENT.CAPTURE.COMPLETED":
		handlePayPalCaptureCompleted(ctx, event.Resource, c.ClientIP())
	case "PAYMENT.CAPTURE.REFUNDED", "PAYMENT.CAPTURE.REVERSED":
		handlePayPalRefund(ctx, event.Resource, c.ClientIP())
	case "CHECKOUT.ORDER.CANCELLED", "CHECKOUT.ORDER.VOIDED":
		handlePayPalOrderCancelled(ctx, event.Resource)
	default:
		logger.LogInfo(ctx, fmt.Sprintf("PayPal webhook 忽略事件 event_type=%s client_ip=%s", event.EventType, c.ClientIP()))
	}

	c.Status(http.StatusOK)
}

func handlePayPalOrderApproved(ctx context.Context, resource json.RawMessage, callerIP string) {
	var order payPalOrderResource
	if err := json.Unmarshal(resource, &order); err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("PayPal order.approved 解析失败 client_ip=%s error=%q", callerIP, err.Error()))
		return
	}
	if order.ID == "" {
		logger.LogWarn(ctx, fmt.Sprintf("PayPal order.approved 缺少 order_id client_ip=%s", callerIP))
		return
	}
	if err := service.CapturePayPalOrder(order.ID); err != nil {
		logger.LogError(ctx, fmt.Sprintf("PayPal capture 失败 order_id=%s client_ip=%s error=%q", order.ID, callerIP, err.Error()))
		return
	}
	logger.LogInfo(ctx, fmt.Sprintf("PayPal capture 成功 order_id=%s client_ip=%s", order.ID, callerIP))
}

func handlePayPalCaptureCompleted(ctx context.Context, resource json.RawMessage, callerIP string) {
	var capture payPalCaptureResource
	if err := json.Unmarshal(resource, &capture); err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("PayPal capture.completed 解析失败 client_ip=%s error=%q", callerIP, err.Error()))
		return
	}

	referenceID := capture.CustomID
	if referenceID == "" {
		logger.LogWarn(ctx, fmt.Sprintf("PayPal capture.completed 缺少 custom_id client_ip=%s", callerIP))
		return
	}

	LockOrder(referenceID)
	defer UnlockOrder(referenceID)

	if order := model.GetSubscriptionOrderByTradeNo(referenceID); order != nil {
		paidAmount, parseErr := strconv.ParseFloat(capture.Amount.Value, 64)
		if parseErr != nil || !strings.EqualFold(strings.TrimSpace(capture.Amount.CurrencyCode), "USD") || math.Abs(paidAmount-order.Money) > 0.005 {
			logger.LogError(ctx, fmt.Sprintf("PayPal 订阅金额校验失败 trade_no=%s expected=%.2f actual=%q currency=%q client_ip=%s", referenceID, order.Money, capture.Amount.Value, capture.Amount.CurrencyCode, callerIP))
			return
		}
	}
	if handled, err := tryCompleteSubscriptionPayment(referenceID, common.GetJsonString(capture), model.PaymentProviderPayPal, model.PaymentMethodPayPal); handled {
		if err != nil {
			logger.LogError(ctx, fmt.Sprintf("PayPal 订阅入账失败 trade_no=%s client_ip=%s error=%q", referenceID, callerIP, err.Error()))
			return
		}
		logger.LogInfo(ctx, fmt.Sprintf("PayPal 订阅支付成功 trade_no=%s amount=%s %s client_ip=%s", referenceID, capture.Amount.Value, capture.Amount.CurrencyCode, callerIP))
		return
	}

	if err := model.RechargePayPal(referenceID, callerIP); err != nil {
		logger.LogError(ctx, fmt.Sprintf("PayPal 充值处理失败 trade_no=%s client_ip=%s error=%q", referenceID, callerIP, err.Error()))
		return
	}
	logger.LogInfo(ctx, fmt.Sprintf("PayPal 充值成功 trade_no=%s amount=%s %s client_ip=%s", referenceID, capture.Amount.Value, capture.Amount.CurrencyCode, callerIP))
}

// handlePayPalRefund reverses a top-up on PayPal refund/reversal. Full refunds
// only; partial refunds are logged for manual review, not auto-processed.
func handlePayPalRefund(ctx context.Context, resource json.RawMessage, callerIP string) {
	var refund payPalRefundResource
	if err := json.Unmarshal(resource, &refund); err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("PayPal refund 解析失败 client_ip=%s error=%q", callerIP, err.Error()))
		return
	}

	referenceID := refund.CustomID
	if referenceID == "" {
		logger.LogWarn(ctx, fmt.Sprintf("PayPal refund 缺少 custom_id refund_id=%s client_ip=%s", refund.ID, callerIP))
		return
	}

	refundUSD, err := parsePayPalRefundUSD(refund.Amount.CurrencyCode, refund.Amount.Value)
	if err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("PayPal refund 金额无效 trade_no=%s refund_id=%s error=%q", referenceID, refund.ID, err.Error()))
		return
	}

	LockOrder(referenceID)
	defer UnlockOrder(referenceID)
	if order := model.GetSubscriptionOrderByTradeNo(referenceID); order != nil {
		if refundUSD+0.001 < order.Money {
			logger.LogError(ctx, fmt.Sprintf("PayPal 订阅部分退款需人工处理 trade_no=%s refund=$%.2f order=$%.2f refund_id=%s client_ip=%s",
				referenceID, refundUSD, order.Money, refund.ID, callerIP))
			return
		}
		if handled, err := tryReverseSubscriptionPayment(referenceID, refundUSD, "refund", common.GetJsonString(refund)); handled {
			if err != nil {
				logger.LogError(ctx, fmt.Sprintf("PayPal 订阅退款处理失败 trade_no=%s refund_id=%s error=%q", referenceID, refund.ID, err.Error()))
			} else {
				logger.LogInfo(ctx, fmt.Sprintf("PayPal 订阅退款处理成功 trade_no=%s refund_id=%s", referenceID, refund.ID))
			}
			return
		}
	}

	// Partial-refund guard: only full refunds are reversed automatically.
	topUp := model.GetTopUpByTradeNo(referenceID)
	if topUp != nil && refundUSD+0.001 < topUp.Money {
		logger.LogError(ctx, fmt.Sprintf("PayPal 部分退款需人工处理 trade_no=%s refund=$%.2f order=$%.2f refund_id=%s client_ip=%s",
			referenceID, refundUSD, topUp.Money, refund.ID, callerIP))
		return
	}

	reversed, userID, err := model.RefundPayPalTopUp(referenceID, refundUSD, callerIP)
	if err != nil {
		if err == model.ErrTopUpStatusInvalid {
			logger.LogInfo(ctx, fmt.Sprintf("PayPal refund 幂等跳过（订单非 success）trade_no=%s refund_id=%s", referenceID, refund.ID))
		} else {
			logger.LogError(ctx, fmt.Sprintf("PayPal 退款处理失败 trade_no=%s refund_id=%s client_ip=%s error=%q", referenceID, refund.ID, callerIP, err.Error()))
		}
		return
	}
	logger.LogInfo(ctx, fmt.Sprintf("PayPal 退款回收成功 trade_no=%s user_id=%d refund=$%.2f 回收额度=%d refund_id=%s client_ip=%s",
		referenceID, userID, refundUSD, reversed, refund.ID, callerIP))
}

func handlePayPalOrderCancelled(ctx context.Context, resource json.RawMessage) {
	var order payPalOrderResource
	if err := json.Unmarshal(resource, &order); err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("PayPal order.cancelled 解析失败 error=%q", err.Error()))
		return
	}
	referenceID := extractPayPalReferenceID(order)
	if referenceID == "" {
		logger.LogWarn(ctx, "PayPal order.cancelled 缺少 reference_id")
		return
	}

	LockOrder(referenceID)
	defer UnlockOrder(referenceID)
	if handled, err := tryExpireSubscriptionPayment(referenceID, model.PaymentProviderPayPal); handled {
		if err != nil {
			logger.LogWarn(ctx, fmt.Sprintf("PayPal 订阅订单过期处理失败 trade_no=%s error=%q", referenceID, err.Error()))
		}
		return
	}

	if err := model.UpdatePendingTopUpStatus(referenceID, model.PaymentProviderPayPal, common.TopUpStatusExpired); err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("PayPal 订单过期处理失败 trade_no=%s error=%q", referenceID, err.Error()))
		return
	}
	logger.LogInfo(ctx, fmt.Sprintf("PayPal 充值订单已过期 trade_no=%s", referenceID))
}

func extractPayPalReferenceID(order payPalOrderResource) string {
	if len(order.PurchaseUnits) == 0 {
		return ""
	}
	if order.PurchaseUnits[0].CustomID != "" {
		return order.PurchaseUnits[0].CustomID
	}
	return order.PurchaseUnits[0].ReferenceID
}

func getPayPalMinTopup() int64 {
	minTopup := setting.PayPalMinTopUp
	if operation_setting.GetQuotaDisplayType() == operation_setting.QuotaDisplayTypeTokens {
		minTopup = minTopup * int(common.QuotaPerUnit)
	}
	return int64(minTopup)
}
