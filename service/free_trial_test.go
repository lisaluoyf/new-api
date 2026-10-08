package service

import (
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
)

func TestFreeTrialRequestModelAccessKeepsOrdinaryKeysOnPaidPath(t *testing.T) {
	truncate(t)
	seedSubscriptionPlan(t, 101, "APIMaster $20 GPT Trial", model.SubscriptionPlanTypeGPTTrial)
	require.NoError(t, model.DB.Model(&model.SubscriptionPlan{}).Where("id = ?", 101).
		Update("model_allowlist", "gpt-6.1-sol").Error)
	seedUserSubscriptionWithPlan(t, 201, 1, 101, 500, 0)
	for _, tc := range []struct {
		group, model       string
		allowed, forbidden bool
	}{
		{"default", "claude-opus-5-5", false, false},
		{"auto", "claude-opus-5-5", false, false},
		{"default", "seedance-2.0", false, false},
		{"Subscription", "claude-opus-5-5", false, true},
		{"Free Trial", "claude-opus-5-5", false, true},
		{"default", "gpt-6.1-sol", true, false},
		{"Subscription", "gpt-6.1-sol", true, false},
	} {
		t.Run(tc.group+"/"+tc.model, func(t *testing.T) {
			access, err := FreeTrialRequestModelAccess(1, tc.model, tc.group)
			require.NoError(t, err)
			require.True(t, access.HasTrial)
			require.Equal(t, tc.allowed, access.Allowed)
			require.Equal(t, tc.forbidden, access.Forbidden)
		})
	}
	access, err := FreeTrialRequestModelAccess(2, "gpt-6.1-sol", "Subscription")
	require.NoError(t, err)
	require.False(t, access.HasTrial)
	require.True(t, access.Forbidden)
}

func TestIsFreeTrialGroup(t *testing.T) {
	require.True(t, IsFreeTrialGroup("Subscription"))
	// Keep historical logs and any in-flight key migration on the trial path.
	require.True(t, IsFreeTrialGroup("Free Trial"))
	require.False(t, IsFreeTrialGroup("default"))
}

func TestIsFreeTrialEligibleModel(t *testing.T) {
	require.True(t, IsFreeTrialEligibleModel("gpt-5"))
	require.True(t, IsFreeTrialEligibleModel("chatgpt-4o-latest"))
	require.True(t, IsFreeTrialEligibleModel("gpt-4.1-mini"))

	require.False(t, IsFreeTrialEligibleModel(""))
	require.False(t, IsFreeTrialEligibleModel("claude-sonnet-4"))
	require.False(t, IsFreeTrialEligibleModel("text-embedding-3-large"))
	require.False(t, IsFreeTrialEligibleModel("gpt-image-2"))
}

func TestFilterFreeTrialModels(t *testing.T) {
	models := []string{
		"gpt-5",
		"claude-sonnet-4",
		"chatgpt-4o-latest",
		"gpt-image-2",
		"gpt-5",
	}

	filtered := FilterFreeTrialModels(models)
	require.Equal(t, []string{"gpt-5", "chatgpt-4o-latest"}, filtered)
}
