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
		common.ApiErrorMsg(c, "参数错误")
		return
	}

	plan, err := model.GetSubscriptionPlanById(req.PlanId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if !plan.Enabled {
		common.ApiErrorMsg(c, "套餐未启用")
		return
	}
	if model.IsGPTPromotionalSubscriptionPlan(plan) {
		common.ApiErrorMsg(c, "试用套餐仅可通过活动领取")
		return
	}
	if plan.PriceAmount < 0.01 {
		common.ApiErrorMsg(c, "套餐金额过低")
		return
	}
	if !operation_setting.ContainsPayMethod(req.PaymentMethod) {
		common.ApiErrorMsg(c, "支付方式不存在")
		return
	}

	userId := c.GetInt("id")
	terms, err := resolveSubscriptionOrderTerms(userId, plan)
	if err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	if terms.Payable < 0.01 {
		common.ApiErrorMsg(c, "应付金额过低")
		return
	}
	chargeAmount, err := calculateSubscriptionEpayChargeAmount(terms.Payable, operation_setting.Price)
	if err != nil {
		common.ApiErrorMsg(c, "人民币支付汇率配置错误")
		return
	}
	if plan.MaxPurchasePerUser > 0 {
		count, err := model.CountUserSubscriptionsByPlan(userId, plan.Id)
		if err != nil {
			common.ApiError(c, err)
			return
		}
		if count >= int64(plan.MaxPurchasePerUser) {
			common.ApiErrorMsg(c, "已达到该套餐购买上限")
			return
		}
	}

	callBackAddress := service.GetCallbackAddress()
	returnUrl, err := url.Parse(callBackAddress + "/api/subscription/epay/return")
	if err != nil {
		common.ApiErrorMsg(c, "回调地址配置错误")
		return
	}
	notifyUrl, err := url.Parse(callBackAddress + "/api/subscription/epay/notify")
	if err != nil {
		common.ApiErrorMsg(c, "回调地址配置错误")
		return
	}

	tradeNo := fmt.Sprintf("%s%d", common.GetRandomString(6), time.Now().Unix())
	tradeNo = fmt.Sprintf("SUBUSR%dNO%s", userId, tradeNo)

	client := GetEpayClient()
	if client == nil {
		common.ApiErrorMsg(c, "当前管理员未配置支付信息")
		return
	}

	order := &model.SubscriptionOrder{
		UserId:                 userId,
		PlanId:                 plan.Id,
		Money:                  terms.Payable,
		ListPrice:              terms.ListPrice,
		CreditAmount:           terms.CreditAmount,
		OrderType:              terms.OrderType,
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
		common.ApiErrorMsg(c, "创建订单失败")
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
			c.Redirect(http.StatusFound, system_setting.ServerAddress+"/freemodel?payment=fail")
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
			c.Redirect(http.StatusFound, system_setting.ServerAddress+"/freemodel?payment=fail")
			return
		}
		providerPayload := buildSubscriptionEpayCompletionPayload(order.ProviderPayload, verifyInfo)
		if err := model.CompleteSubscriptionOrder(verifyInfo.ServiceTradeNo, providerPayload, model.PaymentProviderEpay, verifyInfo.Type); err != nil {
			c.Redirect(http.StatusFound, system_setting.ServerAddress+"/freemodel?payment=fail")
			return
		}
		c.Redirect(http.StatusFound, system_setting.ServerAddress+"/freemodel?payment=success")
		return
	}
	c.Redirect(http.StatusFound, system_setting.ServerAddress+"/freemodel?payment=pending")
}
