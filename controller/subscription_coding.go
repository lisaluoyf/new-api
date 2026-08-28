package controller

import (
	"math"
	"sort"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

type codingModelDTO struct {
	Model      string  `json:"model"`
	Multiplier float64 `json:"multiplier"`
}

type publicCodingPlanDTO struct {
	Id                int              `json:"id,omitempty"`
	Title             string           `json:"title"`
	Subtitle          string           `json:"subtitle,omitempty"`
	PriceAmount       float64          `json:"price_amount"`
	Currency          string           `json:"currency"`
	OfficialAmountUSD float64          `json:"official_amount_usd"`
	DurationDays      int              `json:"duration_days"`
	TierLevel         int              `json:"tier_level"`
	Models            []codingModelDTO `json:"models"`
	Recommended       bool             `json:"recommended"`
	CardDescription   string           `json:"card_description,omitempty"`
	UpdatedAt         int64            `json:"updated_at"`
}

type codingPlanStateDTO struct {
	Subscription *codingSubscriptionDTO `json:"subscription,omitempty"`
	Plan         *publicCodingPlanDTO   `json:"plan,omitempty"`
}

type codingSubscriptionDTO struct {
	Id                    int     `json:"id"`
	PlanId                int     `json:"plan_id"`
	Status                string  `json:"status"`
	StartTime             int64   `json:"start_time"`
	EndTime               int64   `json:"end_time"`
	AmountTotal           int64   `json:"amount_total"`
	AmountUsed            int64   `json:"amount_used"`
	PlanTitleSnapshot     string  `json:"plan_title_snapshot,omitempty"`
	TierLevelSnapshot     int     `json:"tier_level_snapshot"`
	PaidAmountSnapshotUSD float64 `json:"paid_amount_snapshot_usd"`
}

func codingPlanDTO(plan model.SubscriptionPlan, includeID bool) (publicCodingPlanDTO, error) {
	multipliers, err := model.ParseCodingModelMultipliers(plan.CodingModelMultipliers)
	if err != nil {
		return publicCodingPlanDTO{}, err
	}
	models := make([]codingModelDTO, 0, len(multipliers))
	for name, multiplier := range multipliers {
		models = append(models, codingModelDTO{Model: name, Multiplier: multiplier})
	}
	sort.Slice(models, func(i, j int) bool { return models[i].Model < models[j].Model })
	dto := publicCodingPlanDTO{
		Title: plan.Title, Subtitle: plan.Subtitle, PriceAmount: plan.PriceAmount,
		Currency: plan.Currency, OfficialAmountUSD: plan.CodingOfficialAmountUSD,
		DurationDays: 30, TierLevel: plan.TierLevel, Models: models,
		Recommended: plan.Recommended, CardDescription: plan.CardDescription, UpdatedAt: plan.UpdatedAt,
	}
	if includeID {
		dto.Id = plan.Id
	}
	return dto, nil
}

func GetPublicCodingPlanCatalog(c *gin.Context) {
	plans, err := model.GetEnabledCodingPlans()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	result := make([]publicCodingPlanDTO, 0, len(plans))
	var updatedAt int64
	for _, plan := range plans {
		dto, err := codingPlanDTO(plan, false)
		if err != nil {
			continue
		}
		result = append(result, dto)
		updatedAt = max(updatedAt, plan.UpdatedAt)
	}
	common.ApiSuccess(c, gin.H{"plans": result, "updated_at": updatedAt})
}

func GetCodingPlans(c *gin.Context) {
	plans, err := model.GetEnabledCodingPlans()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	result := make([]publicCodingPlanDTO, 0, len(plans))
	for _, plan := range plans {
		dto, err := codingPlanDTO(plan, true)
		if err != nil {
			continue
		}
		result = append(result, dto)
	}
	state, err := model.GetCodingPlanState(c.GetInt("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	stateDTO := codingPlanStateDTO{}
	if state.Subscription != nil {
		stateDTO.Subscription = &codingSubscriptionDTO{
			Id: state.Subscription.Id, PlanId: state.Subscription.PlanId,
			Status: state.Subscription.Status, StartTime: state.Subscription.StartTime,
			EndTime: state.Subscription.EndTime, AmountTotal: state.Subscription.AmountTotal,
			AmountUsed:            state.Subscription.AmountUsed,
			PlanTitleSnapshot:     state.Subscription.PlanTitleSnapshot,
			TierLevelSnapshot:     state.Subscription.TierLevelSnapshot,
			PaidAmountSnapshotUSD: state.Subscription.PaidAmountSnapshot,
		}
	}
	if state.Plan != nil {
		planDTO, err := codingPlanDTO(*state.Plan, true)
		if err != nil {
			common.ApiError(c, err)
			return
		}
		stateDTO.Plan = &planDTO
	}
	common.ApiSuccess(c, gin.H{"plans": result, "state": stateDTO})
}

func GetCodingPlanQuote(c *gin.Context) {
	var req struct {
		PlanId int `json:"plan_id"`
	}
	if c.ShouldBindJSON(&req) != nil || req.PlanId <= 0 {
		common.ApiErrorMsg(c, "Invalid parameters")
		return
	}
	plan, err := model.GetSubscriptionPlanById(req.PlanId)
	if err != nil || !model.IsCodingPlanPlanUsable(plan) {
		common.ApiErrorMsg(c, "Plan is not available")
		return
	}
	orderType, previousId, credit, payable, err := model.CalculateCodingPlanQuote(c.GetInt("id"), plan)
	if err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	if payable < 0.01 || math.IsNaN(payable) || math.IsInf(payable, 0) {
		common.ApiErrorMsg(c, "The remaining value already covers this plan; no payment order is required")
		return
	}
	common.ApiSuccess(c, gin.H{
		"product_type": model.SubscriptionPlanTypeCodingPlan,
		"order_type":   orderType, "previous_subscription_id": previousId,
		"list_price": plan.PriceAmount, "credit_amount": credit, "payable": payable,
		"payment_methods": availableGPTSubscriptionPaymentMethods(c.GetInt("id"), payable),
	})
}
