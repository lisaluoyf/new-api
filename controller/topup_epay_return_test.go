package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/stretchr/testify/require"
)

func TestGetWalletEpayHistoryURL(t *testing.T) {
	oldServerAddress := system_setting.ServerAddress
	system_setting.ServerAddress = "https://apimaster.ai/"
	t.Cleanup(func() {
		system_setting.ServerAddress = oldServerAddress
	})

	require.Equal(t, "https://apimaster.ai/console/wallet?show_history=true", getWalletEpayHistoryURL())
}

func TestGetWalletEpayReturnURL(t *testing.T) {
	oldServerAddress := system_setting.ServerAddress
	system_setting.ServerAddress = "https://apimaster.ai/"
	t.Cleanup(func() {
		system_setting.ServerAddress = oldServerAddress
	})

	require.Equal(t, "https://apimaster.ai/api/user/epay/return", getWalletEpayReturnURL())
}
