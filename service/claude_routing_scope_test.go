package service

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestAnthropicHistoryCapabilityByModelAndProtocol(t *testing.T) {
	models := []string{"claude-fable-5", "claude-fable-5-1", "claude-haiku-4-5", "claude-opus-4-7", "claude-opus-4-8", "claude-opus-5", "claude-opus-5-5", "claude-sonnet-4-6", "claude-sonnet-5", "claude-sonnet-5-5"}
	restricted := append([]string{}, models...)
	// Haiku is verified to accept both tool ID forms on channel 293.
	restricted = append(restricted[:2], restricted[3:]...)
	setting, err := common.Marshal(map[string]any{"anthropic_tool_history_models": restricted})
	require.NoError(t, err)
	ch := &model.Channel{Setting: strPtr(string(setting))}
	for _, name := range models {
		for _, tc := range []struct {
			path, body string
			history    bool
		}{
			{"/v1/chat/completions", `{"messages":[{"role":"user","content":"call_text"}]}`, false},
			{"/v1/chat/completions", `{"messages":[{"role":"assistant","tool_calls":[{"id":"call_a"}]},{"role":"tool","tool_call_id":"call_a","content":"ok"}]}`, true},
			{"/v1/chat/completions", `{"messages":[{"role":"tool","tool_call_id":"call_a","content":"ok"}]}`, true},
			{"/v1/messages", `{"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"call_a"}]}]}`, true},
			{"/v1/messages", `{"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"toolu_a"}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_a","content":"ok"}]}]}`, false},
			{"/v1/responses", `{"input":"hi"}`, false},
			{"/v1/responses", `{"input":[{"type":"function_call","call_id":"call_a"}]}`, true},
			{"/v1/responses", `{"input":[{"type":"function_call_output","call_id":"call_a","output":"ok"}]}`, true},
			{"/v1/responses", `{"input":[{"type":"function_call","call_id":"toolu_a"},{"type":"function_call_output","call_id":"toolu_a","output":"ok"}]}`, false},
		} {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", tc.path, strings.NewReader(tc.body))
			c.Request.Header.Set("Content-Type", "application/json")
			want := !tc.history || name == "claude-haiku-4-5"
			require.Equal(t, want, ChannelPickFilter(c, name)(ch), name+tc.path+tc.body)
			require.Equal(t, want, ValidateChannelClientPolicy(c, ch, name) == nil)
			require.Equal(t, want, ChannelPickFilter(c, name)(ch))
			if name != "claude-haiku-4-5" {
				storage, err := common.GetBodyStorage(c)
				require.NoError(t, err)
				body, err := storage.Bytes()
				require.NoError(t, err)
				require.Equal(t, tc.body, string(body))
			}
			require.True(t, ChannelPickFilter(c, name)(&model.Channel{}))
		}
	}
	// Channel-local aliases must not bypass a model-specific restriction.
	ch.ModelMapping = strPtr(`{"custom-sonnet":"claude-sonnet-5-5"}`)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/responses", strings.NewReader(`{"input":[{"type":"function_call","call_id":"call_a"}]}`))
	require.False(t, ChannelPickFilter(c, "custom-sonnet")(ch))
}

func TestCheapestRoutingRequiresEnabledDefaultAbility(t *testing.T) {
	oldDB := model.DB
	t.Cleanup(func() { model.DB = oldDB; InvalidateChannelRoutingCache() })
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.Ability{}, &model.ChannelModelPricing{}))
	name := "claude-group-isolation-regression"
	for _, tc := range []struct {
		id      int
		group   string
		enabled bool
		price   float64
	}{
		{296, "offical", true, 0.01}, {85, "default", false, 0.02}, {221, "default", true, 0.5}, {38, "default", true, 2},
	} {
		ch := model.Channel{Id: tc.id, Name: tc.group, Key: "test", Models: name, Group: tc.group, Status: 1}
		require.NoError(t, db.Create(&ch).Error)
		require.NoError(t, db.Create(&model.Ability{ChannelId: tc.id, Group: tc.group, Model: name, Enabled: tc.enabled}).Error)
		require.NoError(t, db.Create(&model.ChannelModelPricing{ChannelId: tc.id, ModelName: name, InputPrice: tc.price, PricingSource: "api"}).Error)
	}
	InvalidateChannelRoutingCache()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"messages":[{"role":"user","content":"hi"}]}`))
	retry := 0
	param := &RetryParam{Ctx: c, TokenGroup: AutoCheapestGroup, ModelName: name, Retry: &retry}
	ch, group, err := CacheGetRandomSatisfiedChannel(param)
	require.NoError(t, err)
	require.Equal(t, "default", group)
	require.Equal(t, 221, ch.Id)
	c.Set("use_channel", []string{"221"})
	retry = 1
	ch, _, err = CacheGetRandomSatisfiedChannel(param)
	require.NoError(t, err)
	require.Equal(t, 38, ch.Id)
	c.Set("use_channel", []string{"221", "38"})
	retry = 2
	ch, _, err = CacheGetRandomSatisfiedChannel(param)
	require.Error(t, err)
	require.Nil(t, ch)
}
