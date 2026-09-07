package model

import (
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
)

const (
	legacyReferralInviterCutoff = "2026-09-07T00:00:00Z"
	legacyReferralBonusDuration = 5 * 24 * time.Hour
)

type LegacyReferralTrialCampaign struct {
	InviterCutoffAt time.Time
	BonusStartAt    time.Time
	BonusEndAt      time.Time
	PlanID          int
	CreditUSD       float64
}

func parseLegacyReferralTrialCampaign() (*LegacyReferralTrialCampaign, bool) {
	enabled, err := strconv.ParseBool(os.Getenv("LEGACY_REFERRAL_TRIAL_ENABLED"))
	if err != nil || !enabled {
		return nil, false
	}

	cutoffValue := strings.TrimSpace(os.Getenv("LEGACY_REFERRAL_INVITER_CUTOFF_AT"))
	if cutoffValue == "" {
		cutoffValue = legacyReferralInviterCutoff
	}
	cutoff, cutoffErr := time.Parse(time.RFC3339, cutoffValue)
	start, startErr := time.Parse(
		time.RFC3339,
		strings.TrimSpace(os.Getenv("LEGACY_REFERRAL_BONUS_START_AT")),
	)
	planID, planErr := strconv.Atoi(strings.TrimSpace(os.Getenv("LEGACY_REFERRAL_TRIAL_PLAN_ID")))
	creditUSD := 100.0
	if value := strings.TrimSpace(os.Getenv("LEGACY_REFERRAL_TRIAL_CREDIT_USD")); value != "" {
		creditUSD, err = strconv.ParseFloat(value, 64)
		if err != nil {
			return nil, false
		}
	}
	if cutoffErr != nil || startErr != nil || planErr != nil || planID <= 0 || creditUSD != 100 {
		return nil, false
	}

	end := start.Add(legacyReferralBonusDuration)
	if value := strings.TrimSpace(os.Getenv("LEGACY_REFERRAL_BONUS_END_AT")); value != "" {
		configuredEnd, endErr := time.Parse(time.RFC3339, value)
		if endErr != nil || !configuredEnd.Equal(end) {
			return nil, false
		}
	}

	return &LegacyReferralTrialCampaign{
		InviterCutoffAt: cutoff,
		BonusStartAt:    start,
		BonusEndAt:      end,
		PlanID:          planID,
		CreditUSD:       creditUSD,
	}, true
}

func IsLegacyReferralShareEligible(
	campaign LegacyReferralTrialCampaign,
	inviterCreatedAt time.Time,
	now time.Time,
) bool {
	return inviterCreatedAt.Before(campaign.InviterCutoffAt) &&
		!now.Before(campaign.BonusStartAt) &&
		now.Before(campaign.BonusEndAt)
}

func IsLegacyReferralTrialPlanCompatible(
	campaign LegacyReferralTrialCampaign,
	standardPlan *SubscriptionPlan,
	bonusPlan *SubscriptionPlan,
) bool {
	if standardPlan == nil || bonusPlan == nil || commonQuotaPerUnit() <= 0 {
		return false
	}
	return bonusPlan.Id == campaign.PlanID &&
		bonusPlan.Id != standardPlan.Id &&
		NormalizeSubscriptionPlanType(bonusPlan.PlanType) == SubscriptionPlanTypeGPTTrial &&
		!bonusPlan.Enabled &&
		bonusPlan.TotalAmount == int64(campaign.CreditUSD*float64(commonQuotaPerUnit())) &&
		bonusPlan.DurationUnit == standardPlan.DurationUnit &&
		bonusPlan.DurationValue == standardPlan.DurationValue &&
		bonusPlan.CustomSeconds == standardPlan.CustomSeconds &&
		bonusPlan.QuotaResetPeriod == standardPlan.QuotaResetPeriod &&
		bonusPlan.QuotaResetCustomSeconds == standardPlan.QuotaResetCustomSeconds &&
		bonusPlan.FiveHourAmount == standardPlan.FiveHourAmount &&
		bonusPlan.SevenDayAmount == standardPlan.SevenDayAmount &&
		bonusPlan.ModelAllowlist == standardPlan.ModelAllowlist &&
		bonusPlan.MaxPurchasePerUser == standardPlan.MaxPurchasePerUser &&
		bonusPlan.UpgradeGroup == standardPlan.UpgradeGroup
}

func commonQuotaPerUnit() int {
	return int(common.QuotaPerUnit)
}

func LegacyReferralTrialCampaignReady(standardPlan *SubscriptionPlan) bool {
	campaign, enabled := parseLegacyReferralTrialCampaign()
	if !enabled {
		return false
	}
	bonusPlan, err := GetSubscriptionPlanById(campaign.PlanID)
	return err == nil && IsLegacyReferralTrialPlanCompatible(*campaign, standardPlan, bonusPlan)
}

func ResolveLegacyReferralShareOffer(
	username string,
	now time.Time,
	standardPlan *SubscriptionPlan,
) (float64, string, bool) {
	campaign, enabled := parseLegacyReferralTrialCampaign()
	if !enabled || APIMASTER_PG_DB == nil || username == "" {
		return 0, "", false
	}
	bonusPlan, err := GetSubscriptionPlanById(campaign.PlanID)
	if err != nil || !IsLegacyReferralTrialPlanCompatible(*campaign, standardPlan, bonusPlan) {
		return 0, "", false
	}

	var inviter struct {
		CreatedAt time.Time `gorm:"column:created_at"`
	}
	err = APIMASTER_PG_DB.Raw(
		`SELECT created_at FROM users WHERE REPLACE(id::text, '-', '') LIKE ? AND status = 'active' LIMIT 1`,
		username+"%",
	).Scan(&inviter).Error
	if err != nil || inviter.CreatedAt.IsZero() || !IsLegacyReferralShareEligible(*campaign, inviter.CreatedAt, now) {
		return 0, "", false
	}
	return campaign.CreditUSD, "legacy_inviter_100", true
}
