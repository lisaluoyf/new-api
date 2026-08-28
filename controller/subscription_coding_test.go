package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
)

func TestCodingPlanDTOHidesInternalIDFromPublicCatalog(t *testing.T) {
	plan := model.SubscriptionPlan{
		Id: 201, Title: "Coding Pro", PlanType: model.SubscriptionPlanTypeCodingPlan,
		PriceAmount: 39, Currency: "USD", DurationUnit: model.SubscriptionDurationDay,
		DurationValue: 30, Enabled: true, TierLevel: 2,
		CodingOfficialAmountUSD: 79,
		CodingModelMultipliers:  `{"kimi-k3":0.850,"glm-5":0.350}`,
	}

	publicDTO, err := codingPlanDTO(plan, false)
	require.NoError(t, err)
	require.Zero(t, publicDTO.Id)
	require.Equal(t, "glm-5", publicDTO.Models[0].Model)
	require.Equal(t, "kimi-k3", publicDTO.Models[1].Model)

	authenticatedDTO, err := codingPlanDTO(plan, true)
	require.NoError(t, err)
	require.Equal(t, plan.Id, authenticatedDTO.Id)
}

func TestNewPaidSubscriptionOrderPersistsCodingProductType(t *testing.T) {
	plan := &model.SubscriptionPlan{Id: 301, PlanType: model.SubscriptionPlanTypeCodingPlan}
	terms := subscriptionOrderTerms{
		OrderType: "upgrade", ListPrice: 79, CreditAmount: 20, Payable: 59,
		PreviousSubscriptionId: 44, PreviousEndTime: 1234, PreviousCycleId: 43,
	}

	order := newPaidSubscriptionOrder(7, plan, terms, "coding-order", model.PaymentMethodClink, model.PaymentProviderClink)
	require.Equal(t, model.SubscriptionPlanTypeCodingPlan, order.ProductType)
	require.Equal(t, terms.Payable, order.Money)
	require.Equal(t, terms.PreviousSubscriptionId, order.PreviousSubscriptionId)
}
