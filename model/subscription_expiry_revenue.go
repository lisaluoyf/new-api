package model

import (
	"errors"

	"github.com/QuantumNous/new-api/common"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// SubscriptionExpiryRevenue is an immutable accounting event created when a
// Coding Plan naturally expires with unused allowance. It is reported as a
// separate classification from allowance consumed by API requests.
type SubscriptionExpiryRevenue struct {
	Id                    int64   `json:"id" gorm:"primaryKey;autoIncrement"`
	SubscriptionId        int     `json:"subscription_id" gorm:"uniqueIndex;not null"`
	UserId                int     `json:"user_id" gorm:"index;not null"`
	PlanId                int     `json:"plan_id" gorm:"index;not null"`
	PlanType              string  `json:"plan_type" gorm:"type:varchar(32);index;not null"`
	ExpiredAt             int64   `json:"expired_at" gorm:"type:bigint;index;not null"`
	AmountTotal           int64   `json:"amount_total" gorm:"type:bigint;not null"`
	AmountUsed            int64   `json:"amount_used" gorm:"type:bigint;not null"`
	ExpiredAllowanceQuota int64   `json:"expired_allowance_quota" gorm:"type:bigint;not null"`
	ExpiredAllowanceUSD   float64 `json:"expired_allowance_usd" gorm:"type:decimal(20,10);not null"`
	PaidValueBasisUSD     float64 `json:"paid_value_basis_usd" gorm:"type:decimal(20,10);not null"`
	ExpiredToRevenueUSD   float64 `json:"expired_to_revenue_usd" gorm:"type:decimal(20,10);not null"`
	CreatedAt             int64   `json:"created_at" gorm:"type:bigint;not null"`
}

func (r *SubscriptionExpiryRevenue) BeforeCreate(tx *gorm.DB) error {
	if r.CreatedAt == 0 {
		r.CreatedAt = common.GetTimestamp()
	}
	return nil
}

func codingPlanPaidValueBasis(sub *UserSubscription, plan *SubscriptionPlan) decimal.Decimal {
	if sub == nil {
		return decimal.Zero
	}
	basis := sub.PaidAmountSnapshot
	if basis <= 0 {
		basis = sub.PriceAmountSnapshot
	}
	if basis <= 0 && plan != nil {
		basis = plan.PriceAmount
	}
	if basis <= 0 {
		return decimal.Zero
	}
	return decimal.NewFromFloat(basis)
}

func buildCodingPlanExpiryRevenue(sub *UserSubscription, plan *SubscriptionPlan) (*SubscriptionExpiryRevenue, bool) {
	if sub == nil || !IsCodingPlan(plan) || sub.AmountTotal <= 0 || common.QuotaPerUnit <= 0 {
		return nil, false
	}
	remaining := sub.AmountTotal - sub.AmountUsed
	if remaining <= 0 {
		return nil, false
	}
	if remaining > sub.AmountTotal {
		remaining = sub.AmountTotal
	}
	basis := codingPlanPaidValueBasis(sub, plan)
	allowanceUSD := decimal.NewFromInt(remaining).Div(decimal.NewFromFloat(common.QuotaPerUnit))
	expiredRevenue := decimal.Zero
	if basis.IsPositive() {
		expiredRevenue = basis.Mul(decimal.NewFromInt(remaining)).Div(decimal.NewFromInt(sub.AmountTotal))
	}
	return &SubscriptionExpiryRevenue{
		SubscriptionId:        sub.Id,
		UserId:                sub.UserId,
		PlanId:                sub.PlanId,
		PlanType:              SubscriptionPlanTypeCodingPlan,
		ExpiredAt:             sub.EndTime,
		AmountTotal:           sub.AmountTotal,
		AmountUsed:            sub.AmountUsed,
		ExpiredAllowanceQuota: remaining,
		ExpiredAllowanceUSD:   allowanceUSD.Round(10).InexactFloat64(),
		PaidValueBasisUSD:     basis.Round(10).InexactFloat64(),
		ExpiredToRevenueUSD:   expiredRevenue.Round(10).InexactFloat64(),
	}, true
}

func recordCodingPlanExpiryRevenuesTx(tx *gorm.DB, subscriptions []UserSubscription) error {
	if tx == nil || len(subscriptions) == 0 {
		return nil
	}
	planIds := make([]int, 0, len(subscriptions))
	seen := make(map[int]struct{}, len(subscriptions))
	for _, sub := range subscriptions {
		if _, ok := seen[sub.PlanId]; ok {
			continue
		}
		seen[sub.PlanId] = struct{}{}
		planIds = append(planIds, sub.PlanId)
	}
	var plans []SubscriptionPlan
	if err := tx.Where("id IN ?", planIds).Find(&plans).Error; err != nil {
		return err
	}
	byId := make(map[int]*SubscriptionPlan, len(plans))
	for i := range plans {
		byId[plans[i].Id] = &plans[i]
	}
	for i := range subscriptions {
		event, ok := buildCodingPlanExpiryRevenue(&subscriptions[i], byId[subscriptions[i].PlanId])
		if !ok {
			continue
		}
		if err := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "subscription_id"}},
			DoNothing: true,
		}).Create(event).Error; err != nil && !errors.Is(err, gorm.ErrDuplicatedKey) {
			return err
		}
	}
	return nil
}
