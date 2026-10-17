package model

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestPreserveAnthropicRoutingSettingsOnChannelEdit(t *testing.T) {
	origin := &Channel{Setting: common.GetPointer(`{"anthropic_tool_history_models":["claude-sonnet-5-5"],"native_anthropic_only":true,"proxy":"old"}`)}
	for _, tc := range []struct{ name, incoming, want string }{
		{"editor omits capabilities", `{"proxy":"new","pass_through_body_enabled":false}`, `{"proxy":"new","pass_through_body_enabled":false,"anthropic_tool_history_models":["claude-sonnet-5-5"],"native_anthropic_only":true}`},
		{"explicitly clear", `{"anthropic_tool_history_models":[],"native_anthropic_only":false}`, `{"anthropic_tool_history_models":[],"native_anthropic_only":false}`},
		{"explicit null", `{"anthropic_tool_history_models":null,"native_anthropic_only":false}`, `{"anthropic_tool_history_models":null,"native_anthropic_only":false}`},
		{"replace models", `{"anthropic_tool_history_models":["claude-opus-5-5"]}`, `{"anthropic_tool_history_models":["claude-opus-5-5"],"native_anthropic_only":true}`},
		{"empty settings", ``, `{"anthropic_tool_history_models":["claude-sonnet-5-5"],"native_anthropic_only":true}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ch := &Channel{Setting: common.GetPointer(tc.incoming)}
			require.NoError(t, ch.PreserveAnthropicRoutingSettings(origin))
			require.JSONEq(t, tc.want, *ch.Setting)
			require.Equal(t, "old", origin.GetSetting().Proxy)
		})
	}
	ch := &Channel{}
	require.NoError(t, ch.PreserveAnthropicRoutingSettings(origin))
	require.Nil(t, ch.Setting, "unrelated PATCH must leave setting omitted")
}
