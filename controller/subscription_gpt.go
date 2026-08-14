package controller

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
)

type gptSubscriptionPaymentMethod struct {
	Provider      string `json:"provider"`
	PaymentMethod string `json:"payment_method"`
	Name          string `json:"name"`
	Description   string `json:"description,omitempty"`
}

func availableGPTSubscriptionPaymentMethods(userID int, payable float64) []gptSubscriptionPaymentMethod {
	if payable < 0.01 {
		return nil
	}
	if forbidden, err := model.IsUserTopupForbidden(userID); err == nil && forbidden {
		return nil
	}

	methods := make([]gptSubscriptionPaymentMethod, 0, 8)
	if isPayPalTopUpEnabled() && payable+0.005 >= float64(setting.PayPalMinTopUp) {
		methods = append(methods, gptSubscriptionPaymentMethod{
			Provider: model.PaymentProviderPayPal, PaymentMethod: model.PaymentMethodPayPal,
			Name: "PayPal", Description: "Credit / Debit",
		})
	}
	if isWaffoPancakeTopUpEnabled() &&
		strings.EqualFold(strings.TrimSpace(setting.WaffoPancakeCurrency), "USD") &&
		payable+0.005 >= float64(setting.WaffoPancakeMinTopUp) {
		methods = append(methods, gptSubscriptionPaymentMethod{
			Provider: model.PaymentProviderWaffoPancake, PaymentMethod: model.PaymentMethodWaffoPancake,
			Name: "Waffo 支付", Description: "Card · Apple Pay · Google Pay",
		})
	}
	if isPlategaTopUpEnabled() && payable+0.005 >= float64(setting.PlategaMinTopUp) {
		methods = append(methods, gptSubscriptionPaymentMethod{
			Provider: model.PaymentProviderPlatega, PaymentMethod: model.PaymentMethodPlatega,
			Name: "俄罗斯 SBP 扫码支付", Description: "СБП / QR",
		})
	}
	if isClinkTopUpEnabled() &&
		strings.EqualFold(strings.TrimSpace(setting.ClinkCurrency), "USD") &&
		payable+0.005 >= float64(setting.ClinkMinTopUp) {
		methods = append(methods, gptSubscriptionPaymentMethod{
			Provider: model.PaymentProviderClink, PaymentMethod: model.PaymentMethodClink,
			Name: "Clink", Description: "Global cards and local methods",
		})
	}

	// Crypto is intentionally governed by the same account-level top-up guard
	// as the wallet page and has no separate provider switch there.
	methods = append(methods, gptSubscriptionPaymentMethod{
		Provider: model.PaymentProviderCrypto, PaymentMethod: model.PaymentMethodCrypto,
		Name: "Crypto", Description: "USDT / USDC",
	})

	if isEpayTopUpEnabled() {
		for _, method := range operation_setting.PayMethods {
			paymentType := strings.TrimSpace(method["type"])
			if paymentType != "alipay" && paymentType != "wxpay" {
				continue
			}
			name := strings.TrimSpace(method["name"])
			if name == "" {
				if paymentType == "alipay" {
					name = "支付宝"
				} else {
					name = "微信支付"
				}
			}
			methods = append(methods, gptSubscriptionPaymentMethod{
				Provider: model.PaymentProviderEpay, PaymentMethod: paymentType, Name: name,
			})
		}
	}
	return methods
}

type subscriptionOrderTerms struct {
	OrderType              string
	PreviousSubscriptionId int
	ListPrice              float64
	CreditAmount           float64
	Payable                float64
	PreviousEndTime        int64
	PreviousCycleId        int
}

func resolveSubscriptionOrderTerms(userId int, plan *model.SubscriptionPlan) (subscriptionOrderTerms, error) {
	terms := subscriptionOrderTerms{OrderType: "purchase", ListPrice: plan.PriceAmount, Payable: plan.PriceAmount}
	if !model.IsGPTPaidSubscriptionPlan(plan) {
		return terms, nil
	}
	access, err := model.GetGPTSubscriptionAccess(userId)
	if err != nil {
		return terms, err
	}
	if !access.CanPurchase {
		return terms, errors.New("Free Model purchases are currently closed")
	}
	terms.OrderType, terms.PreviousSubscriptionId, terms.CreditAmount, terms.Payable, err = model.CalculateGPTSubscriptionQuote(userId, plan)
	if err == nil && terms.PreviousSubscriptionId > 0 {
		var previous model.UserSubscription
		if queryErr := model.DB.Where("id = ? AND user_id = ?", terms.PreviousSubscriptionId, userId).First(&previous).Error; queryErr != nil {
			return terms, queryErr
		}
		terms.PreviousEndTime = previous.EndTime
		terms.PreviousCycleId = previous.CurrentCycleId
	}
	return terms, err
}

type updateGPTSubscriptionAccessRequest struct {
	PublicEnabled bool     `json:"public_enabled"`
	Whitelist     []string `json:"whitelist"`
}

func GetGPTSubscriptionAccess(c *gin.Context) {
	access, err := model.GetGPTSubscriptionAccess(c.GetInt("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	access.Whitelist = nil
	common.ApiSuccess(c, access)
}

func AdminGetGPTSubscriptionAccess(c *gin.Context) {
	common.ApiSuccess(c, model.GPTSubscriptionAccess{
		PublicEnabled: model.GPTSubscriptionPublicEnabled(),
		Whitelist:     model.GPTSubscriptionWhitelist(),
	})
}

func AdminUpdateGPTSubscriptionAccess(c *gin.Context) {
	var req updateGPTSubscriptionAccessRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorMsg(c, "参数错误")
		return
	}
	for _, email := range req.Whitelist {
		if trimmed := strings.TrimSpace(email); trimmed != "" && !strings.Contains(trimmed, "@") {
			common.ApiErrorMsg(c, "白名单邮箱格式错误")
			return
		}
	}
	if err := model.UpdateGPTSubscriptionAccessConfig(req.PublicEnabled, req.Whitelist); err != nil {
		common.ApiError(c, err)
		return
	}
	AdminGetGPTSubscriptionAccess(c)
}

func GetGPTSubscriptionPlans(c *gin.Context) {
	access, err := model.GetGPTSubscriptionAccess(c.GetInt("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if !access.Allowed {
		c.JSON(http.StatusForbidden, gin.H{"success": false, "message": "Free Model is not available for this account"})
		return
	}
	plans, err := model.GetEnabledGPTSubscriptionPlans()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	state, err := model.GetGPTSubscriptionState(c.GetInt("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"plans": plans, "state": state, "access": access})
}

func GetGPTSubscriptionQuote(c *gin.Context) {
	access, err := model.GetGPTSubscriptionAccess(c.GetInt("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if !access.CanPurchase {
		c.JSON(http.StatusForbidden, gin.H{"success": false, "message": "Free Model purchases are currently closed"})
		return
	}
	var req struct {
		PlanId int `json:"plan_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.PlanId <= 0 {
		common.ApiErrorMsg(c, "参数错误")
		return
	}
	plan, err := model.GetSubscriptionPlanById(req.PlanId)
	if err != nil || plan == nil || !plan.Enabled || !model.IsGPTPaidSubscriptionPlan(plan) {
		common.ApiErrorMsg(c, "套餐不可用")
		return
	}
	orderType, previousId, credit, payable, err := model.CalculateGPTSubscriptionQuote(c.GetInt("id"), plan)
	if err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	common.ApiSuccess(c, gin.H{
		"order_type": orderType, "previous_subscription_id": previousId,
		"list_price": plan.PriceAmount, "credit_amount": credit, "payable": payable,
		"payment_methods": availableGPTSubscriptionPaymentMethods(c.GetInt("id"), payable),
	})
}

func AdminGetUserGPTSubscriptionDetails(c *gin.Context) {
	userID, err := strconv.Atoi(c.Param("id"))
	if err != nil || userID <= 0 {
		common.ApiErrorMsg(c, "无效的用户ID")
		return
	}
	state, err := model.GetGPTSubscriptionState(userID)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	var orders []model.SubscriptionOrder
	if err := model.DB.Table("subscription_orders AS so").
		Select("so.*").
		Joins("JOIN subscription_plans AS sp ON sp.id = so.plan_id").
		Where("so.user_id = ? AND sp.plan_type = ?", userID, model.SubscriptionPlanTypeGPTSubscription).
		Order("so.id desc").Scan(&orders).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	type totals struct {
		Revenue float64 `json:"official_revenue_usd"`
		Cost    float64 `json:"channel_cost_usd"`
	}
	var usage totals
	if model.LOG_DB != nil {
		_ = model.LOG_DB.Model(&model.Log{}).
			Select("COALESCE(SUM(quota), 0) / ? AS revenue, COALESCE(SUM(accounting_channel_cost_amount_usd), 0) AS cost", common.QuotaPerUnit).
			Where("user_id = ? AND accounting_status = ? AND other LIKE ?", userID, "ok", "%\"subscription_type\":\"gpt_subscription\"%").
			Scan(&usage).Error
	}
	common.ApiSuccess(c, gin.H{"state": state, "orders": orders, "usage": usage})
}

func AdminReverseGPTSubscriptionOrder(c *gin.Context) {
	tradeNo := strings.TrimSpace(c.Param("trade_no"))
	var req struct {
		Type   string  `json:"type"`
		Amount float64 `json:"amount"`
		Note   string  `json:"note"`
	}
	if tradeNo == "" || c.ShouldBindJSON(&req) != nil || req.Amount <= 0 {
		common.ApiErrorMsg(c, "参数错误")
		return
	}
	order := model.GetSubscriptionOrderByTradeNo(tradeNo)
	if order == nil {
		common.ApiError(c, model.ErrSubscriptionOrderNotFound)
		return
	}
	plan, err := model.GetSubscriptionPlanById(order.PlanId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if !model.IsGPTPaidSubscriptionPlan(plan) {
		common.ApiErrorMsg(c, "订单不是 GPT 付费订阅订单")
		return
	}
	if err := model.ReverseSubscriptionOrder(tradeNo, req.Amount, strings.ToLower(strings.TrimSpace(req.Type)), req.Note); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, nil)
}
