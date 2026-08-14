package controller

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
	"github.com/stripe/stripe-go/v81"
	"github.com/stripe/stripe-go/v81/checkout/session"
	"github.com/thanhpk/randstr"
)

type SubscriptionStripePayRequest struct {
	PlanId int `json:"plan_id"`
}

func SubscriptionRequestStripePay(c *gin.Context) {
	var req SubscriptionStripePayRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.PlanId <= 0 {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}

	plan, err := model.GetSubscriptionPlanById(req.PlanId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if !plan.Enabled {
		common.ApiErrorI18n(c, i18n.MsgSubscriptionNotEnabled)
		return
	}
	if model.IsGPTPromotionalSubscriptionPlan(plan) {
		common.ApiErrorMsg(c, "试用套餐仅可通过活动领取")
		return
	}
	if plan.StripePriceId == "" && !model.IsGPTPaidSubscriptionPlan(plan) {
		common.ApiErrorI18n(c, i18n.MsgPaymentPriceIdNotConfig)
		return
	}
	if !strings.HasPrefix(setting.StripeApiSecret, "sk_") && !strings.HasPrefix(setting.StripeApiSecret, "rk_") {
		common.ApiErrorI18n(c, i18n.MsgPaymentStripeNotConfig)
		return
	}
	if setting.StripeWebhookSecret == "" {
		common.ApiErrorI18n(c, i18n.MsgPaymentWebhookNotConfig)
		return
	}

	userId := c.GetInt("id")
	user, err := model.GetUserById(userId, false)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if user == nil {
		common.ApiErrorI18n(c, i18n.MsgUserNotExists)
		return
	}
	terms, err := resolveSubscriptionOrderTerms(userId, plan)
	if err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}

	if plan.MaxPurchasePerUser > 0 {
		count, err := model.CountUserSubscriptionsByPlan(userId, plan.Id)
		if err != nil {
			common.ApiError(c, err)
			return
		}
		if count >= int64(plan.MaxPurchasePerUser) {
			common.ApiErrorI18n(c, i18n.MsgSubscriptionPurchaseMax)
			return
		}
	}

	reference := fmt.Sprintf("sub-stripe-ref-%d-%d-%s", user.Id, time.Now().UnixMilli(), randstr.String(4))
	referenceId := "sub_ref_" + common.Sha1([]byte(reference))

	payLink, err := genStripeSubscriptionLink(referenceId, user.StripeCustomer, user.Email, plan, terms.Payable)
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Stripe 订阅支付链接创建失败 trade_no=%s plan_id=%d error=%q", referenceId, plan.Id, err.Error()))
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": i18n.T(c, i18n.MsgPaymentStartFailed)})
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
		TradeNo:                referenceId,
		PaymentMethod:          model.PaymentMethodStripe,
		PaymentProvider:        model.PaymentProviderStripe,
		CreateTime:             time.Now().Unix(),
		Status:                 common.TopUpStatusPending,
	}
	if err := order.Insert(); err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": i18n.T(c, i18n.MsgPaymentCreateFailed)})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "success",
		"data": gin.H{
			"pay_link": payLink,
		},
	})
}

func genStripeSubscriptionLink(referenceId string, customerId string, email string, plan *model.SubscriptionPlan, payable float64) (string, error) {
	stripe.Key = setting.StripeApiSecret

	params := &stripe.CheckoutSessionParams{
		ClientReferenceID: stripe.String(referenceId),
		SuccessURL:        stripe.String(system_setting.ServerAddress + "/freemodel?payment=success"),
		CancelURL:         stripe.String(system_setting.ServerAddress + "/freemodel?payment=cancelled"),
	}
	if model.IsGPTPaidSubscriptionPlan(plan) {
		params.Mode = stripe.String(string(stripe.CheckoutSessionModePayment))
		params.LineItems = []*stripe.CheckoutSessionLineItemParams{{
			Quantity: stripe.Int64(1),
			PriceData: &stripe.CheckoutSessionLineItemPriceDataParams{
				Currency:    stripe.String(strings.ToLower(plan.Currency)),
				UnitAmount:  stripe.Int64(int64(payable*100 + 0.5)),
				ProductData: &stripe.CheckoutSessionLineItemPriceDataProductDataParams{Name: stripe.String("Free Model · " + plan.Title)},
			},
		}}
		params.PaymentIntentData = &stripe.CheckoutSessionPaymentIntentDataParams{
			Metadata: map[string]string{"trade_no": referenceId, "product_type": model.SubscriptionPlanTypeGPTSubscription},
		}
	} else {
		params.Mode = stripe.String(string(stripe.CheckoutSessionModeSubscription))
		params.LineItems = []*stripe.CheckoutSessionLineItemParams{{Price: stripe.String(plan.StripePriceId), Quantity: stripe.Int64(1)}}
	}

	if "" == customerId {
		if "" != email {
			params.CustomerEmail = stripe.String(email)
		}
		params.CustomerCreation = stripe.String(string(stripe.CheckoutSessionCustomerCreationAlways))
	} else {
		params.Customer = stripe.String(customerId)
	}

	result, err := session.New(params)
	if err != nil {
		return "", err
	}
	return result.URL, nil
}
