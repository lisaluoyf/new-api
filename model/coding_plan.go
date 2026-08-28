package model

import (
	"errors"
	"math"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

type CodingPlanState struct {
	Subscription *UserSubscription `json:"subscription,omitempty"`
	Plan         *SubscriptionPlan `json:"plan,omitempty"`
}

func GetEnabledCodingPlans() ([]SubscriptionPlan, error) {
	var plans []SubscriptionPlan
	err := DB.Where("plan_type = ? AND enabled = ? AND price_amount > ? AND coding_official_amount_usd > ?", SubscriptionPlanTypeCodingPlan, true, 0, 0).
		Order("sort_order desc, tier_level asc, id asc").Find(&plans).Error
	return plans, err
}

func activeCodingPlanTx(tx *gorm.DB, userId int) (*UserSubscription, *SubscriptionPlan, error) {
	if tx == nil {
		tx = DB
	}
	var subs []UserSubscription
	now := GetDBTimestampTx(tx)
	if err := tx.Where("user_id = ? AND status = ? AND end_time > ?", userId, "active", now).
		Order("end_time asc, id asc").Find(&subs).Error; err != nil {
		return nil, nil, err
	}
	var selected *UserSubscription
	var selectedPlan *SubscriptionPlan
	for i := range subs {
		plan, err := getSubscriptionPlanByIdTx(tx, subs[i].PlanId)
		if err != nil {
			return nil, nil, err
		}
		if !IsCodingPlan(plan) || !IsCodingPlanPlanUsable(plan) {
			continue
		}
		if selected == nil || plan.TierLevel > selectedPlan.TierLevel {
			selected = &subs[i]
			selectedPlan = plan
		}
	}
	if selected == nil {
		return nil, nil, gorm.ErrRecordNotFound
	}
	return selected, selectedPlan, nil
}

func IsCodingPlanPlanUsable(plan *SubscriptionPlan) bool {
	return plan != nil && plan.Enabled && plan.CodingOfficialAmountUSD > 0 && plan.TotalAmount > 0
}

func GetCodingPlanState(userId int) (CodingPlanState, error) {
	sub, plan, err := activeCodingPlanTx(DB, userId)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return CodingPlanState{}, nil
	}
	return CodingPlanState{Subscription: sub, Plan: plan}, err
}

func GetActiveCodingPlanForModel(userId int, modelName string) (*SubscriptionPlan, error) {
	if userId <= 0 {
		return nil, errors.New("invalid user id")
	}
	var subs []UserSubscription
	now := GetDBTimestamp()
	if err := DB.Where("user_id = ? AND status = ? AND end_time > ?", userId, "active", now).
		Order("end_time asc, id asc").Find(&subs).Error; err != nil {
		return nil, err
	}
	var selected *SubscriptionPlan
	for i := range subs {
		plan, err := GetSubscriptionPlanById(subs[i].PlanId)
		if err != nil {
			return nil, err
		}
		if !IsCodingPlanPlanUsable(plan) || !IsCodingPlanModelAllowed(plan, modelName) {
			continue
		}
		if selected == nil || plan.TierLevel > selected.TierLevel {
			selected = plan
		}
	}
	if selected == nil {
		return nil, gorm.ErrRecordNotFound
	}
	return selected, nil
}

func CalculateCodingPlanQuote(userId int, target *SubscriptionPlan) (orderType string, previousId int, credit, payable float64, err error) {
	if target == nil || !IsCodingPlan(target) || !IsCodingPlanPlanUsable(target) {
		return "", 0, 0, 0, errors.New("invalid Coding Plan")
	}
	orderType, payable = "purchase", target.PriceAmount
	current, currentPlan, findErr := activeCodingPlanTx(DB, userId)
	if errors.Is(findErr, gorm.ErrRecordNotFound) {
		return orderType, 0, 0, payable, nil
	}
	if findErr != nil {
		return "", 0, 0, 0, findErr
	}
	previousId = current.Id
	if current.PlanId != target.Id {
		currentLevel := current.TierLevelSnapshot
		if currentLevel == 0 {
			currentLevel = currentPlan.TierLevel
		}
		if target.TierLevel <= currentLevel {
			return "", previousId, 0, 0, errors.New("active Coding Plan can only renew or upgrade")
		}
		orderType = "upgrade"
	}

	total := current.AmountTotal
	if total <= 0 {
		total = int64(math.Round(currentPlan.CodingOfficialAmountUSD * common.QuotaPerUnit))
	}
	remaining := total - current.AmountUsed
	if remaining < 0 {
		remaining = 0
	}
	paid := current.PaidAmountSnapshot
	if paid <= 0 {
		paid = current.PriceAmountSnapshot
	}
	targetPrice := decimal.NewFromFloat(target.PriceAmount)
	creditAmount := decimal.Zero
	if total > 0 && paid > 0 {
		creditAmount = decimal.NewFromFloat(paid).
			Mul(decimal.NewFromInt(remaining)).
			Div(decimal.NewFromInt(total))
	}
	if creditAmount.GreaterThan(targetPrice) {
		creditAmount = targetPrice
	}
	if creditAmount.IsNegative() {
		creditAmount = decimal.Zero
	}
	payableAmount := targetPrice.Sub(creditAmount)
	if payableAmount.IsNegative() {
		payableAmount = decimal.Zero
	}
	credit = creditAmount.Round(6).InexactFloat64()
	payable = payableAmount.Round(6).InexactFloat64()
	return orderType, previousId, credit, payable, nil
}

func completeCodingPlanOrderTx(tx *gorm.DB, order *SubscriptionOrder, plan *SubscriptionPlan) (*UserSubscription, error) {
	if tx == nil || order == nil || plan == nil || !IsCodingPlan(plan) {
		return nil, errors.New("invalid Coding Plan completion")
	}
	now := GetDBTimestampTx(tx)
	if order.OrderType == "purchase" {
		if _, _, err := activeCodingPlanTx(tx, order.UserId); err == nil {
			return nil, errors.New("user already has an active Coding Plan")
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
	}
	if order.OrderType == "renewal" || order.OrderType == "upgrade" {
		var current UserSubscription
		if err := tx.Set("gorm:query_option", "FOR UPDATE").Where("id = ? AND user_id = ?", order.PreviousSubscriptionId, order.UserId).First(&current).Error; err != nil {
			return nil, err
		}
		if current.Status != "active" || current.EndTime <= now {
			return nil, errors.New("previous Coding Plan is no longer active")
		}
		if err := tx.Model(&current).Updates(map[string]any{"status": "cancelled", "end_time": now, "updated_at": now}).Error; err != nil {
			return nil, err
		}
	}
	created, err := CreateUserSubscriptionFromPlanTx(tx, order.UserId, plan, "order")
	if err != nil {
		return nil, err
	}
	created.PaidAmountSnapshot = order.Money
	created.CurrentCycleId = order.Id
	if err := tx.Save(created).Error; err != nil {
		return nil, err
	}
	return created, nil
}

func CodingPlanDuration() time.Duration { return 30 * 24 * time.Hour }
