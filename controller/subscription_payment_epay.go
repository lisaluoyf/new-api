package controller

import (
	"fmt"
	"math"
	"net/http"
	"net/url"
	"time"

	"github.com/Calcium-Ion/go-epay/epay"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
	"github.com/samber/lo"
	"github.com/shopspring/decimal"
)

type SubscriptionEpayPayRequest struct {
	PlanId        int    `json:"plan_id"`
	PaymentMethod string `json:"payment_method"`
}

type subscriptionEpayPaymentSnapshot struct {
	PayableUSD     float64 `json:"payable_usd"`
	ExchangeRate   float64 `json:"exchange_rate"`
	ChargeAmount   string  `json:"charge_amount"`
	ChargeCurrency string  `json:"charge_currency"`
}

func calculateSubscriptionEpayChargeAmount(payableUSD float64, exchangeRate float64) (string, error) {
	if payableUSD < 0.01 || exchangeRate <= 0 || math.IsNaN(payableUSD) || math.IsInf(payableUSD, 0) || math.IsNaN(exchangeRate) || math.IsInf(exchangeRate, 0) {
		return "", fmt.Errorf("invalid subscription EPay amount")
	}
	amount := decimal.NewFromFloat(payableUSD).
		Mul(decimal.NewFromFloat(exchangeRate)).
		Round(2)
	if amount.LessThan(decimal.NewFromFloat(0.01)) {
		return "", fmt.Errorf("subscription EPay amount is too low")
	}
	return amount.StringFixed(2), nil
}

func verifySubscriptionEpayPaymentSnapshot(snapshotJSON string, callbackAmount string) error {
	var snapshot subscriptionEpayPaymentSnapshot
	if err := common.UnmarshalJsonStr(snapshotJSON, &snapshot); err != nil {
		return fmt.Errorf("invalid subscription EPay payment snapshot: %w", err)
	}
	if snapshot.ChargeCurrency != "CNY" || snapshot.ChargeAmount == "" {
		return fmt.Errorf("missing subscription EPay payment snapshot")
	}
	expected, err := decimal.NewFromString(snapshot.ChargeAmount)
	if err != nil {
		return fmt.Errorf("invalid expected subscription EPay amount: %w", err)
	}
	actual, err := decimal.NewFromString(callbackAmount)
	if err != nil {
		return fmt.Errorf("invalid callback subscription EPay amount: %w", err)
	}
	if !expected.Round(2).Equal(actual.Round(2)) {
		return fmt.Errorf("subscription EPay amount mismatch: expected %s CNY, got %s CNY", expected.StringFixed(2), actual.StringFixed(2))
	}
	return nil
}

func verifySubscriptionEpayCallbackAmount(tradeNo string, callbackAmount string) (*model.SubscriptionOrder, error) {
	order := model.GetSubscriptionOrderByTradeNo(tradeNo)
	if order == nil {
		return nil, model.ErrSubscriptionOrderNotFound
	}
	if order.PaymentProvider != model.PaymentProviderEpay {
		return nil, model.ErrPaymentMethodMismatch
	}
	if order.Status == common.TopUpStatusSuccess {
		return order, nil
	}
	return order, verifySubscriptionEpayPaymentSnapshot(order.ProviderPayload, callbackAmount)
}

func buildSubscriptionEpayCompletionPayload(snapshotJSON string, verifyInfo *epay.VerifyRes) string {
	var snapshot subscriptionEpayPaymentSnapshot
	if err := common.UnmarshalJsonStr(snapshotJSON, &snapshot); err != nil {
		return common.GetJsonString(verifyInfo)
	}
	return common.GetJsonString(map[string]any{
		"payment_snapshot": snapshot,
		"callback":         verifyInfo,
	})
}

func SubscriptionRequestEpay(c *gin.Context) {
	if abortIfTopupForbidden(c) {
		return
	}
	var req SubscriptionEpayPayRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.PlanId <= 0 {
		common.ApiErrorMsg(c, "Invalid parameters")
		return
	}

	plan, err := model.GetSubscriptionPlanById(req.PlanId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if !plan.Enabled {
		common.ApiErrorMsg(c, "Plan is not enabled")
		return
	}
	if model.IsGPTPromotionalSubscriptionPlan(plan) {
		common.ApiErrorMsg(c, "Trial plans can only be claimed through the promotion")
		return
	}
	if plan.PriceAmount < 0.01 {
		common.ApiErrorMsg(c, "Plan price is too low")
		return
	}
	if !operation_setting.ContainsPayMethod(req.PaymentMethod) {
		common.ApiErrorMsg(c, "Payment method does not exist")
		return
	}

	userId := c.GetInt("id")
	terms, err := resolveSubscriptionOrderTerms(userId, plan)
	if err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	if terms.Payable < 0.01 {
		common.ApiErrorMsg(c, "Payable amount is too low")
		return
	}
	chargeAmount, err := calculateSubscriptionEpayChargeAmount(terms.Payable, operation_setting.Price)
	if err != nil {
		common.ApiErrorMsg(c, "CNY exchange rate is not configured correctly")
		return
	}
	if plan.MaxPurchasePerUser > 0 {
		count, err := model.CountUserSubscriptionsByPlan(userId, plan.Id)
		if err != nil {
			common.ApiError(c, err)
			return
		}
		if count >= int64(plan.MaxPurchasePerUser) {
			common.ApiErrorMsg(c, "You have reached the purchase limit for this plan")
			return
		}
	}

	callBackAddress := service.GetCallbackAddress()
	returnUrl, err := url.Parse(callBackAddress + "/api/subscription/epay/return")
	if err != nil {
		common.ApiErrorMsg(c, "Callback URL is misconfigured")
		return
	}
	notifyUrl, err := url.Parse(callBackAddress + "/api/subscription/epay/notify")
	if err != nil {
		common.ApiErrorMsg(c, "Callback URL is misconfigured")
		return
	}

	tradeNo := fmt.Sprintf("%s%d", common.GetRandomString(6), time.Now().Unix())
	tradeNo = fmt.Sprintf("SUBUSR%dNO%s", userId, tradeNo)

	client := GetEpayClient()
	if client == nil {
		common.ApiErrorMsg(c, "Payment configuration is missing")
		return
	}

	order := &model.SubscriptionOrder{
		UserId:                 userId,
		PlanId:                 plan.Id,
		Money:                  terms.Payable,
		ListPrice:              terms.ListPrice,
		CreditAmount:           terms.CreditAmount,
		OrderType:              terms.OrderType,
		ProductType:            model.NormalizeSubscriptionPlanType(plan.PlanType),
		PreviousSubscriptionId: terms.PreviousSubscriptionId,
		PreviousEndTime:        terms.PreviousEndTime,
		PreviousCycleId:        terms.PreviousCycleId,
		TradeNo:                tradeNo,
		PaymentMethod:          req.PaymentMethod,
		PaymentProvider:        model.PaymentProviderEpay,
		ProviderPayload: common.GetJsonString(subscriptionEpayPaymentSnapshot{
			PayableUSD: terms.Payable, ExchangeRate: operation_setting.Price,
			ChargeAmount: chargeAmount, ChargeCurrency: "CNY",
		}),
		CreateTime: time.Now().Unix(),
		Status:     common.TopUpStatusPending,
	}
	if err := order.Insert(); err != nil {
		common.ApiErrorMsg(c, "Failed to create order")
		return
	}
	uri, params, err := client.Purchase(&epay.PurchaseArgs{
		Type:           req.PaymentMethod,
		ServiceTradeNo: tradeNo,
		Name:           fmt.Sprintf("SUB:%s", plan.Title),
		Money:          chargeAmount,
		Device:         epay.PC,
		NotifyUrl:      notifyUrl,
		ReturnUrl:      returnUrl,
	})
	if err != nil {
		_ = model.ExpireSubscriptionOrder(tradeNo, model.PaymentProviderEpay)
		common.ApiErrorMsg(c, i18n.T(c, i18n.MsgPaymentStartFailed))
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "success", "data": params, "url": uri})
}

func SubscriptionEpayNotify(c *gin.Context) {
	var params map[string]string

	if c.Request.Method == "POST" {
		// POST 请求：从 POST body 解析参数
		if err := c.Request.ParseForm(); err != nil {
			_, _ = c.Writer.Write([]byte("fail"))
			return
		}
		params = lo.Reduce(lo.Keys(c.Request.PostForm), func(r map[string]string, t string, i int) map[string]string {
			r[t] = c.Request.PostForm.Get(t)
			return r
		}, map[string]string{})
	} else {
		// GET 请求：从 URL Query 解析参数
		params = lo.Reduce(lo.Keys(c.Request.URL.Query()), func(r map[string]string, t string, i int) map[string]string {
			r[t] = c.Request.URL.Query().Get(t)
			return r
		}, map[string]string{})
	}

	if len(params) == 0 {
		_, _ = c.Writer.Write([]byte("fail"))
		return
	}

	client := GetEpayClient()
	if client == nil {
		_, _ = c.Writer.Write([]byte("fail"))
		return
	}
	verifyInfo, err := client.Verify(params)
	if err != nil || !verifyInfo.VerifyStatus {
		_, _ = c.Writer.Write([]byte("fail"))
		return
	}

	if verifyInfo.TradeStatus != epay.StatusTradeSuccess {
		_, _ = c.Writer.Write([]byte("fail"))
		return
	}

	LockOrder(verifyInfo.ServiceTradeNo)
	defer UnlockOrder(verifyInfo.ServiceTradeNo)
	order, err := verifySubscriptionEpayCallbackAmount(verifyInfo.ServiceTradeNo, verifyInfo.Money)
	if err != nil {
		common.SysLog(fmt.Sprintf("subscription EPay notify amount validation failed trade_no=%s error=%q", verifyInfo.ServiceTradeNo, err.Error()))
		_, _ = c.Writer.Write([]byte("fail"))
		return
	}

	providerPayload := buildSubscriptionEpayCompletionPayload(order.ProviderPayload, verifyInfo)
	if err := model.CompleteSubscriptionOrder(verifyInfo.ServiceTradeNo, providerPayload, model.PaymentProviderEpay, verifyInfo.Type); err != nil {
		_, _ = c.Writer.Write([]byte("fail"))
		return
	}

	_, _ = c.Writer.Write([]byte("success"))
}

// SubscriptionEpayReturn handles browser return after payment.
// It verifies the payload and completes the order, then redirects to console.
func SubscriptionEpayReturn(c *gin.Context) {
	var params map[string]string

	if c.Request.Method == "POST" {
		// POST 请求：从 POST body 解析参数
		if err := c.Request.ParseForm(); err != nil {
			c.Redirect(http.StatusFound, freeModelPaymentURL("fail"))
			return
		}
		params = lo.Reduce(lo.Keys(c.Request.PostForm), func(r map[string]string, t string, i int) map[string]string {
			r[t] = c.Request.PostForm.Get(t)
			return r
		}, map[string]string{})
	} else {
		// GET 请求：从 URL Query 解析参数
		params = lo.Reduce(lo.Keys(c.Request.URL.Query()), func(r map[string]string, t string, i int) map[string]string {
			r[t] = c.Request.URL.Query().Get(t)
			return r
		}, map[string]string{})
	}

	if len(params) == 0 {
		c.Redirect(http.StatusFound, system_setting.ServerAddress+"/freemodel?payment=fail")
		return
	}

	client := GetEpayClient()
	if client == nil {
		c.Redirect(http.StatusFound, system_setting.ServerAddress+"/freemodel?payment=fail")
		return
	}
	verifyInfo, err := client.Verify(params)
	if err != nil || !verifyInfo.VerifyStatus {
		c.Redirect(http.StatusFound, system_setting.ServerAddress+"/freemodel?payment=fail")
		return
	}
	if verifyInfo.TradeStatus == epay.StatusTradeSuccess {
		LockOrder(verifyInfo.ServiceTradeNo)
		defer UnlockOrder(verifyInfo.ServiceTradeNo)
		order, err := verifySubscriptionEpayCallbackAmount(verifyInfo.ServiceTradeNo, verifyInfo.Money)
		if err != nil {
			common.SysLog(fmt.Sprintf("subscription EPay return amount validation failed trade_no=%s error=%q", verifyInfo.ServiceTradeNo, err.Error()))
			c.Redirect(http.StatusFound, subscriptionOrderPaymentURL(order, "fail"))
			return
		}
		providerPayload := buildSubscriptionEpayCompletionPayload(order.ProviderPayload, verifyInfo)
		if err := model.CompleteSubscriptionOrder(verifyInfo.ServiceTradeNo, providerPayload, model.PaymentProviderEpay, verifyInfo.Type); err != nil {
			c.Redirect(http.StatusFound, subscriptionOrderPaymentURL(order, "fail"))
			return
		}
		c.Redirect(http.StatusFound, subscriptionOrderPaymentURL(order, "success"))
		return
	}
	c.Redirect(http.StatusFound, subscriptionOrderPaymentURL(model.GetSubscriptionOrderByTradeNo(verifyInfo.ServiceTradeNo), "pending"))
}
