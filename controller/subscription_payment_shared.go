package controller

import (
	"errors"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/system_setting"
)

func resolveGPTSubscriptionPayment(userID int, planID int) (*model.SubscriptionPlan, subscriptionOrderTerms, error) {
	if userID <= 0 || planID <= 0 {
		return nil, subscriptionOrderTerms{}, errors.New("Invalid parameters")
	}
	plan, err := model.GetSubscriptionPlanById(planID)
	if err != nil || plan == nil || !plan.Enabled || !model.IsGPTPaidSubscriptionPlan(plan) {
		return nil, subscriptionOrderTerms{}, errors.New("Plan is not available")
	}
	terms, err := resolveSubscriptionOrderTerms(userID, plan)
	if err != nil {
		return nil, subscriptionOrderTerms{}, err
	}
	if terms.Payable < 0.01 {
		return nil, subscriptionOrderTerms{}, errors.New("Payable amount is too low")
	}
	return plan, terms, nil
}

func newGPTSubscriptionOrder(
	userID int,
	plan *model.SubscriptionPlan,
	terms subscriptionOrderTerms,
	tradeNo string,
	paymentMethod string,
	paymentProvider string,
) *model.SubscriptionOrder {
	return &model.SubscriptionOrder{
		UserId:                 userID,
		PlanId:                 plan.Id,
		Money:                  terms.Payable,
		ListPrice:              terms.ListPrice,
		CreditAmount:           terms.CreditAmount,
		OrderType:              terms.OrderType,
		PreviousSubscriptionId: terms.PreviousSubscriptionId,
		PreviousEndTime:        terms.PreviousEndTime,
		PreviousCycleId:        terms.PreviousCycleId,
		TradeNo:                tradeNo,
		PaymentMethod:          paymentMethod,
		PaymentProvider:        paymentProvider,
		CreateTime:             common.GetTimestamp(),
		Status:                 common.TopUpStatusPending,
	}
}

func freeModelPaymentURL(status string) string {
	base := strings.TrimRight(system_setting.ServerAddress, "/") + "/freemodel"
	if strings.TrimSpace(status) == "" {
		return base
	}
	return fmt.Sprintf("%s?payment=%s", base, status)
}

// tryCompleteSubscriptionPayment returns handled=false only when tradeNo does
// not belong to a subscription order. All other errors are subscription-order
// errors and must not fall through to wallet crediting.
func tryCompleteSubscriptionPayment(
	tradeNo string,
	providerPayload string,
	expectedProvider string,
	actualPaymentMethod string,
) (handled bool, err error) {
	err = model.CompleteSubscriptionOrder(tradeNo, providerPayload, expectedProvider, actualPaymentMethod)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, model.ErrSubscriptionOrderNotFound) {
		return false, nil
	}
	return true, err
}

func tryExpireSubscriptionPayment(tradeNo string, expectedProvider string) (handled bool, err error) {
	err = model.ExpireSubscriptionOrder(tradeNo, expectedProvider)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, model.ErrSubscriptionOrderNotFound) {
		return false, nil
	}
	return true, err
}

func tryReverseSubscriptionPayment(
	tradeNo string,
	amount float64,
	reversalType string,
	providerPayload string,
) (handled bool, err error) {
	err = model.ReverseSubscriptionOrder(tradeNo, amount, reversalType, providerPayload)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, model.ErrSubscriptionOrderNotFound) {
		return false, nil
	}
	return true, err
}
