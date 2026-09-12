package operation_setting

import (
	"testing"

	"github.com/QuantumNous/new-api/setting/config"
	"github.com/stretchr/testify/require"
)

func TestUpstreamFalseSuccessSettingDefaultsToEnabled(t *testing.T) {
	original := IsUpstreamFalseSuccessEnabled()
	t.Cleanup(func() { SetUpstreamFalseSuccessEnabled(original) })

	require.True(t, GetUpstreamFalseSuccessSetting().Enabled)
	require.True(t, IsUpstreamFalseSuccessEnabled())
}

func TestUpstreamFalseSuccessSettingLoadsFromDatabaseOptions(t *testing.T) {
	original := IsUpstreamFalseSuccessEnabled()
	t.Cleanup(func() { SetUpstreamFalseSuccessEnabled(original) })

	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"upstream_false_success_setting.enabled": "false",
	}))
	require.False(t, IsUpstreamFalseSuccessEnabled())

	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"upstream_false_success_setting.enabled": "true",
	}))
	require.True(t, IsUpstreamFalseSuccessEnabled())
}
