package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEffectiveModelPriceRatio(t *testing.T) {
	setting := `{"manual_group_ratio":2,"model_price_ratio":0}`
	require.Equal(t, 1.0, effectiveModelPriceRatio(&setting, 2))
	require.Equal(t, 0.0, effectiveModelPriceRatio(&setting, 0))

	settingWithRatio := `{"manual_group_ratio":2,"model_price_ratio":0.8}`
	require.Equal(t, 0.8, effectiveModelPriceRatio(&settingWithRatio, 2))
}

func TestExtractManualGroupRatioAndKeyGroup(t *testing.T) {
	setting := `{"key_group":"Claude Max（仅限CC）","manual_group_ratio":2}`
	require.Equal(t, "Claude Max（仅限CC）", ExtractKeyGroup(&setting))
	require.Equal(t, 2.0, ExtractManualGroupRatio(&setting))
}

func TestResolveChannelGroupRatioManualValueOverridesUpstream(t *testing.T) {
	setting := `{"manual_group_ratio":0.02}`
	require.InDelta(t, 0.02, resolveChannelGroupRatio(&setting, 1.05), 0.000001)
}

func TestResolveChannelGroupRatioZeroOrMissingUsesUpstream(t *testing.T) {
	zero := `{"manual_group_ratio":0}`
	empty := `{}`
	require.InDelta(t, 1.05, resolveChannelGroupRatio(&zero, 1.05), 0.000001)
	require.InDelta(t, 1.05, resolveChannelGroupRatio(&empty, 1.05), 0.000001)
}

func TestEffectiveChannelPriceRatioPrefersModelPriceRatio(t *testing.T) {
	setting := `{"model_price_ratio":1.5}`
	legacy := 2.0
	require.InDelta(t, 1.5, EffectiveChannelPriceRatio(&setting, &legacy), 0.000001)

	empty := `{}`
	require.InDelta(t, 2.0, EffectiveChannelPriceRatio(&empty, &legacy), 0.000001)
	require.InDelta(t, 1.0, EffectiveChannelPriceRatio(nil, nil), 0.000001)
}
