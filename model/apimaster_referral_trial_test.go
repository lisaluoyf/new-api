package model

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestLegacyReferralShareEligibilityBoundaries(t *testing.T) {
	campaign := LegacyReferralTrialCampaign{
		InviterCutoffAt: time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC),
		BonusStartAt:    time.Date(2026, 9, 7, 4, 0, 0, 0, time.UTC),
		BonusEndAt:      time.Date(2026, 9, 12, 4, 0, 0, 0, time.UTC),
		PlanID:          123,
		CreditUSD:       100,
	}

	require.True(t, IsLegacyReferralShareEligible(
		campaign,
		time.Date(2026, 9, 6, 23, 59, 59, 0, time.UTC),
		campaign.BonusStartAt,
	))
	require.False(t, IsLegacyReferralShareEligible(
		campaign,
		campaign.InviterCutoffAt,
		campaign.BonusStartAt,
	))
	require.False(t, IsLegacyReferralShareEligible(
		campaign,
		time.Date(2026, 9, 6, 23, 59, 59, 0, time.UTC),
		campaign.BonusEndAt,
	))
}

func TestLegacyReferralTrialPlanCompatibility(t *testing.T) {
	campaign := LegacyReferralTrialCampaign{PlanID: 2, CreditUSD: 100}
	standard := &SubscriptionPlan{
		Id: 1, PlanType: SubscriptionPlanTypeGPTTrial, Enabled: true,
		DurationUnit: SubscriptionDurationDay, DurationValue: 5,
		QuotaResetPeriod: SubscriptionResetNever, FiveHourAmount: 10,
		SevenDayAmount: 20, ModelAllowlist: "gpt-6-astra", MaxPurchasePerUser: 1,
	}
	bonus := *standard
	bonus.Id = 2
	bonus.Enabled = false
	bonus.TotalAmount = int64(100 * commonQuotaPerUnit())

	require.True(t, IsLegacyReferralTrialPlanCompatible(campaign, standard, &bonus))
	bonus.ModelAllowlist = "different-model"
	require.False(t, IsLegacyReferralTrialPlanCompatible(campaign, standard, &bonus))
}
