package controller

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/stretchr/testify/require"
)

func TestRelayProbeTargetPreservesProtocol(t *testing.T) {
	for _, tc := range []struct {
		format   types.RelayFormat
		endpoint constant.EndpointType
	}{
		{types.RelayFormatOpenAI, constant.EndpointTypeOpenAI},
		{types.RelayFormatOpenAIResponses, constant.EndpointTypeOpenAIResponse},
		{types.RelayFormatOpenAIResponsesCompaction, constant.EndpointTypeOpenAIResponseCompact},
		{types.RelayFormatClaude, constant.EndpointTypeAnthropic},
		{types.RelayFormatGemini, constant.EndpointTypeGemini},
	} {
		for _, stream := range []bool{false, true} {
			target := relayProbeTarget(&relaycommon.RelayInfo{RelayFormat: tc.format, OriginModelName: "gpt-5.6-terra", IsStream: stream})
			require.Equal(t, tc.endpoint, target.EndpointType)
			require.Equal(t, stream, target.IsStream)
			require.True(t, target.Valid())
		}
	}
}

func TestDisableAndRecoveryProbeUseFailingResponsesStream(t *testing.T) {
	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = oldTimeout })
	initModelListColumnNames(t)
	db := setupModelDataToggleTestDB(t)
	oldRatios := ratio_setting.ModelRatio2JSONString()
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"gpt-5.6-terra":1}`))
	t.Cleanup(func() { _ = ratio_setting.UpdateModelRatioByJSONString(oldRatios) })
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Log{}, &model.ChannelModelPricing{}))
	oldLogDB, oldRedis := model.LOG_DB, common.RedisEnabled
	model.LOG_DB, common.RedisEnabled = db, false
	t.Cleanup(func() { model.LOG_DB, common.RedisEnabled = oldLogDB, oldRedis })
	require.NoError(t, db.Create(&model.User{Id: 1, Username: "root", Role: common.RoleRootUser, Group: "default", Quota: 1000000}).Error)
	t.Setenv("FEISHU_CHANNEL_CHAT_ID", "")
	t.Setenv("FEISHU_OPS_CHAT_ID", "")
	if service.GetHttpClient() == nil {
		service.InitHttpClient()
	}
	var mu sync.Mutex
	var paths []string
	var modes []bool
	responsesHealthy := false
	chatHealthy := true
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model  string `json:"model"`
			Stream bool   `json:"stream"`
		}
		if err := common.DecodeJson(r.Body, &body); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		mu.Lock()
		paths = append(paths, r.URL.Path)
		modes = append(modes, body.Stream)
		healthy := responsesHealthy
		chatOK := chatHealthy
		mu.Unlock()
		if r.URL.Path == "/v1/chat/completions" {
			if !chatOK {
				http.Error(w, `{"error":{"message":"chat unavailable","type":"server_error"}}`, http.StatusBadGateway)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"id":"chatcmpl-test","object":"chat.completion","model":"gpt-5.6-terra","choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":7,"completion_tokens":1,"total_tokens":8}}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if !healthy {
			_, _ = io.WriteString(w, "data: {\"type\":\"response.failed\",\"response\":{\"id\":\"resp-test\",\"status\":\"failed\",\"error\":{\"type\":\"server_error\",\"message\":\"upstream stream failed\"}}}\n\n")
			return
		}
		_, _ = io.WriteString(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hi\"}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp-test\",\"status\":\"completed\",\"model\":\"gpt-5.6-terra\",\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"hi\"}]}],\"usage\":{\"input_tokens\":7,\"output_tokens\":1,\"total_tokens\":8}}}\n\n")
	}))
	defer upstream.Close()
	baseURL, autoBan := upstream.URL, 1
	channel := &model.Channel{Id: 163, Type: constant.ChannelTypeOpenAI, Name: "probe-regression", Status: common.ChannelStatusEnabled, AutoBan: &autoBan, BaseURL: &baseURL, Key: "test-key", Models: "gpt-5.6-terra,gpt-5.6-sol", Group: "default"}
	require.NoError(t, channel.Insert())
	// The legacy default passes, reproducing the false recovery signal.
	result, _ := probeChannelForAutomation(channel, "gpt-5.6-terra")
	require.NoError(t, result.localErr)
	require.Nil(t, result.newAPIError)
	target := types.ChannelProbeTarget{ModelName: "gpt-5.6-terra", EndpointType: constant.EndpointTypeOpenAIResponse, IsStream: true}
	channelErr := types.ChannelError{ChannelId: channel.Id, ChannelName: channel.Name, ChannelType: channel.Type, AutoBan: true}
	apiErr := types.NewOpenAIError(errors.New("upstream stream failed"), types.ErrorCodeBadResponse, 502)
	probeBeforeDisablingChannel(channelErr, apiErr, "five stream failures", target.ModelName, target)
	current, err := model.GetChannelById(channel.Id, true)
	require.NoError(t, err)
	require.Contains(t, current.GetDisabledModels(), target.ModelName)
	require.NotContains(t, current.GetDisabledModels(), "gpt-5.6-sol")
	saved, err := current.AutoDisabledModelProbeTargets(target.ModelName)
	require.NoError(t, err)
	require.Equal(t, []types.ChannelProbeTarget{target}, saved)
	result, _ = probeAutoDisabledModel(current, target.ModelName)
	require.NotNil(t, result.newAPIError, "recovery must not fall back to non-stream chat")
	chatTarget := types.ChannelProbeTarget{ModelName: target.ModelName, EndpointType: constant.EndpointTypeOpenAI}
	service.DisableChannelModel(channelErr, target.ModelName, "chat also failed", chatTarget)
	current, err = model.GetChannelById(channel.Id, true)
	require.NoError(t, err)
	mu.Lock()
	responsesHealthy = true
	chatHealthy = false
	mu.Unlock()
	result, _ = probeAutoDisabledModel(current, target.ModelName)
	require.NotNil(t, result.newAPIError, "all persisted targets must pass before recovery")
	mu.Lock()
	chatHealthy = true
	mu.Unlock()
	result, _ = probeAutoDisabledModel(current, target.ModelName)
	require.NoError(t, result.localErr)
	require.Nil(t, result.newAPIError)
	service.EnableChannelModel(current.Id, target.ModelName, current.Name, current.AutoDisabledModelVersion(target.ModelName))
	current, err = model.GetChannelById(channel.Id, true)
	require.NoError(t, err)
	require.NotContains(t, current.GetDisabledModels(), target.ModelName)
	mu.Lock()
	require.Equal(t, []string{"/v1/chat/completions", "/v1/responses", "/v1/responses", "/v1/responses", "/v1/chat/completions", "/v1/responses", "/v1/chat/completions"}, paths)
	require.Equal(t, []bool{false, true, true, true, false, true, false}, modes)
	mu.Unlock()
	_, err = model.SetChannelModelsManuallyDisabled(channel.Id, []string{target.ModelName}, true, 42)
	require.NoError(t, err)
	service.EnableChannelModel(channel.Id, target.ModelName, channel.Name)
	current, err = model.GetChannelById(channel.Id, true)
	require.NoError(t, err)
	require.Contains(t, current.GetManuallyDisabledModels(), target.ModelName)
}

func TestInvalidSavedProbeTargetDoesNotUseLegacyProbe(t *testing.T) {
	channel := &model.Channel{}
	channel.SetOtherInfo(map[string]interface{}{"auto_disabled_models": map[string]interface{}{"gpt-5.6-terra": map[string]interface{}{"probe_targets": []interface{}{}}}})
	result, _ := probeAutoDisabledModel(channel, "gpt-5.6-terra")
	require.Error(t, result.localErr)
}
