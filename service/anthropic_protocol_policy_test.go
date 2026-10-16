package service

import (
	"io"
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

func TestNativeAnthropicRoutingPolicy(t *testing.T) {
	native := &model.Channel{Setting: strPtr(`{"native_anthropic_only":true}`)}
	generic := &model.Channel{}
	for _, tc := range []struct {
		name, path, body string
		want             bool
	}{
		{"chat tools", "/v1/chat/completions", `{"messages":[{"role":"assistant","tool_calls":[{"id":"call_test"}]}]}`, false},
		{"chat text", "/v1/chat/completions", `{"messages":[{"role":"user","content":"hi"}]}`, false},
		{"responses", "/v1/responses", `{"input":"hi"}`, false},
		{"messages text", "/v1/messages", `{"messages":[{"role":"user","content":"hi"}]}`, true},
		{"native tool round trip", "/v1/messages", `{"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"toolu_test","name":"lookup","input":{}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_test","content":"ok"}]}]}`, true},
		{"converted tool use", "/v1/messages", `{"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"call_test"}]}]}`, false},
		{"converted tool result", "/v1/messages", `{"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_test"}]}]}`, false},
		{"OpenAI history on native path", "/v1/messages", `{"messages":[{"role":"assistant","tool_calls":[{"id":"call_test"}]}]}`, false},
		{"call prefix in text is harmless", "/v1/messages", `{"messages":[{"role":"user","content":[{"type":"text","text":"call_test"}]}]}`, true},
		{"bad native JSON", "/v1/messages", `{`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", tc.path, strings.NewReader(tc.body))
			c.Request.Header.Set("Content-Type", "application/json")
			// Client identity cannot bypass the original protocol restriction.
			c.Request.Header.Set("User-Agent", "claude-cli/1.0")
			c.Request.Header.Set("anthropic-beta", "claude-code-20250219")
			filter := ChannelPickFilter(c, "custom-alias")
			require.Equal(t, tc.want, filter(native))
			require.Equal(t, tc.want, ValidateChannelClientPolicy(c, native, "custom-alias") == nil)
			require.True(t, filter(generic))
			require.NoError(t, ValidateChannelClientPolicy(c, generic, "custom-alias"))
			// Repeated selections must not consume or rewrite history or IDs.
			require.Equal(t, tc.want, ChannelPickFilter(c, "custom-alias")(native))
			if tc.path == "/v1/messages" {
				storage, err := common.GetBodyStorage(c)
				require.NoError(t, err)
				bytes, err := storage.Bytes()
				require.NoError(t, err)
				require.Equal(t, tc.body, string(bytes))
			} else {
				bytes, err := io.ReadAll(c.Request.Body)
				require.NoError(t, err)
				require.Equal(t, tc.body, string(bytes))
			}
		})
	}
}

func TestNativeAnthropicCheapestSelectionAndRetry(t *testing.T) {
	oldDB := model.DB
	t.Cleanup(func() { model.DB = oldDB })
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.ChannelModelPricing{}, &model.Ability{}))
	name := "claude-native-routing-regression"
	native := model.Channel{Name: "native", Key: "test", Models: name, Status: 1, Group: AutoCheapestGroup, Setting: strPtr(`{"native_anthropic_only":true}`)}
	generic := model.Channel{Name: "compatible", Key: "test", Models: name, Status: 1, Group: AutoCheapestGroup}
	require.NoError(t, db.Create(&native).Error)
	require.NoError(t, db.Create(&generic).Error)
	require.NoError(t, db.Create(&[]model.ChannelModelPricing{
		{ChannelId: native.Id, ModelName: name, InputPrice: 0.1, GroupRatio: 1, PricingSource: "api"},
		{ChannelId: generic.Id, ModelName: name, InputPrice: 1, GroupRatio: 1, PricingSource: "api"},
	}).Error)
	for _, path := range []string{"/v1/chat/completions", "/v1/responses", "/v1/messages"} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("POST", path, strings.NewReader(`{"messages":[{"role":"user","content":"hello"}]}`))
		c.Request.Header.Set("Content-Type", "application/json")
		ch, err := SelectCheapestEnabledChannelExcludingWithFilter(name, nil, ChannelPickFilter(c, name))
		require.NoError(t, err)
		if path == "/v1/messages" {
			require.Equal(t, native.Id, ch.Id)
			next, err := SelectCheapestEnabledChannelExcludingWithFilter(name, []int{native.Id}, ChannelPickFilter(c, name))
			require.NoError(t, err)
			require.Equal(t, generic.Id, next.Id)
		} else {
			require.Equal(t, generic.Id, ch.Id)
			_, err = SelectCheapestEnabledChannelExcludingWithFilter(name, []int{generic.Id}, ChannelPickFilter(c, name))
			require.ErrorIs(t, err, ErrNoCheapestChannel)
		}
	}
}

func TestNativeAnthropicPrioritySelectionAndRetry(t *testing.T) {
	oldDB, oldMemory := model.DB, common.MemoryCacheEnabled
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	t.Cleanup(func() { model.DB = oldDB; common.MemoryCacheEnabled = oldMemory; model.InitChannelCache() })
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.Ability{}))
	name := "claude-native-priority-regression"
	for id := 1; id <= 3; id++ {
		priority := int64(100 - id)
		ch := model.Channel{Id: id, Name: "protocol-test", Key: "test", Models: name, Group: "protocol-test", Status: 1, Priority: &priority}
		if id == 1 {
			ch.Setting = strPtr(`{"native_anthropic_only":true}`)
		}
		require.NoError(t, db.Create(&ch).Error)
		require.NoError(t, ch.AddAbilities(nil))
	}
	for _, memory := range []bool{true} {
		common.MemoryCacheEnabled = memory
		model.InitChannelCache()
		for _, path := range []string{"/v1/chat/completions", "/v1/messages"} {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", path, strings.NewReader(`{"messages":[{"role":"user","content":"hello"}]}`))
			c.Request.Header.Set("Content-Type", "application/json")
			want := 2
			if path == "/v1/messages" {
				want = 1
			}
			for retry := 0; retry < 2; retry++ {
				ch, _, err := CacheGetRandomSatisfiedChannel(&RetryParam{Ctx: c, TokenGroup: "protocol-test", ModelName: name, Retry: &retry})
				require.NoError(t, err)
				require.NotNil(t, ch)
				require.Equal(t, want+retry, ch.Id)
			}
		}
	}
}
