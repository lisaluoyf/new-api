package controller

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"
	"github.com/stretchr/testify/require"
)

func TestImageProbeSizeContract(t *testing.T) {
	for _, tc := range []struct {
		name             string
		id               int
		model            string
		format           dto.GptImage2SizeFormat
		size, resolution string
	}{
		{"sunburst APIMart legacy", 81, "gpt-image-2.5-sunburst", "", "1:1", "1k"},
		{"flare APIMart legacy", 59, "gpt-image-2.5-flare", "", "1:1", "1k"},
		{"image2 APIMart legacy", 81, "gpt-image-2", "", "1:1", "1k"},
		{"declared ratio", 900, "gpt-image-2.5-sunburst", dto.GptImage2SizeFormatAspectRatioWithResolution, "1:1", "1k"},
		{"declared pixels override legacy", 81, "gpt-image-2.5-sunburst", dto.GptImage2SizeFormatPixelDimensions, "1024x1024", ""},
		{"other provider unchanged", 73, "gpt-image-2.5-sunburst", "", "1024x1024", ""},
		{"other model unchanged", 81, "dall-e-3", "", "1024x1024", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ch := &model.Channel{Id: tc.id}
			if tc.format != "" {
				ch.SetOtherSettings(dto.ChannelOtherSettings{GptImage2Capabilities: &dto.GptImage2Capabilities{SizeFormat: tc.format}})
			}
			req := buildTestRequest(tc.model, string(constant.EndpointTypeImageGeneration), ch, false).(*dto.ImageRequest)
			require.Equal(t, tc.size, req.Size)
			require.Equal(t, tc.resolution, req.Resolution)
		})
	}
}

func TestSunburstRecoveryProbeUsesSupportedSize(t *testing.T) {
	initModelListColumnNames(t)
	db := setupModelDataToggleTestDB(t)
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
	received := make(chan dto.ImageRequest, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req dto.ImageRequest
		if common.DecodeJson(r.Body, &req) != nil || req.Size != "1:1" || req.Resolution != "1k" {
			http.Error(w, `{"error":{"message":"pixel sizes are not supported","type":"invalid_request_error"}}`, 400)
			return
		}
		received <- req
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"created":1,"data":[{"url":"https://example.com/cat.png"}]}`)
	}))
	defer upstream.Close()
	base, autoBan := upstream.URL, 1
	ch := &model.Channel{Id: 81, Type: constant.ChannelTypeOpenAI, Name: "image-size-recovery", Status: common.ChannelStatusEnabled, AutoBan: &autoBan, BaseURL: &base, Key: "test-key", Models: "gpt-image-2.5-sunburst", Group: "default"}
	require.NoError(t, ch.Insert())
	target := types.ChannelProbeTarget{ModelName: ch.Models, EndpointType: constant.EndpointTypeImageGeneration}
	service.DisableChannelModel(types.ChannelError{ChannelId: 81, ChannelName: ch.Name, ChannelType: ch.Type, AutoBan: true}, ch.Models, "upstream failed", target)
	current, err := model.GetChannelById(81, true)
	require.NoError(t, err)
	require.Contains(t, current.GetDisabledModels(), ch.Models)
	result, _ := probeAutoDisabledModel(current, ch.Models)
	require.NoError(t, result.localErr)
	require.Nil(t, result.newAPIError)
	require.Equal(t, ch.Models, (<-received).Model)
	service.EnableChannelModel(81, ch.Models, ch.Name, current.AutoDisabledModelVersion(ch.Models))
	current, err = model.GetChannelById(81, true)
	require.NoError(t, err)
	require.NotContains(t, current.GetDisabledModels(), ch.Models)
}
