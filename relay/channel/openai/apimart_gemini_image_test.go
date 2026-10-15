package openai

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAPIMartGeminiImageConversion(t *testing.T) {
	for _, host := range []string{"api.apib.ai", "api.apimart.ai"} {
		for _, mode := range []int{relayconstant.RelayModeImagesGenerations, relayconstant.RelayModeImagesEdits} {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", "/v1/images/edits", strings.NewReader("{}"))
			c.Request.Header.Set("Content-Type", "application/json")
			info := &relaycommon.RelayInfo{OriginModelName: "gemini-3-pro-image", RelayMode: mode, RequestURLPath: c.Request.URL.Path, ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI, ChannelBaseUrl: "https://" + host, UpstreamModelName: "gemini-3-pro-image-preview"}}
			refs := []string{"https://example.com/ref.png", "data:image/png;base64,aGVsbG8="}
			original := dto.ImageRequest{Model: info.UpstreamModelName, Prompt: "keep reference", ImageUrls: refs, Size: "1:1", Resolution: "2K", N: common.GetPointer(uint(1)), ResponseFormat: "b64_json"}
			got, err := (&Adaptor{}).ConvertImageRequest(c, info, original)
			require.NoError(t, err)
			wire := got.(dto.ImageRequest)
			require.Equal(t, refs, wire.ImageUrls)
			require.Equal(t, "url", wire.ResponseFormat)
			require.Equal(t, "b64_json", c.GetString(apimartGeminiResponseFormatKey))
			require.Equal(t, "2K", wire.Resolution)
			require.Equal(t, info.UpstreamModelName, wire.Model)
			path, err := (&Adaptor{}).GetRequestURL(info)
			require.NoError(t, err)
			require.Equal(t, "https://"+host+"/v1/images/generations", path)
			require.Equal(t, "b64_json", original.ResponseFormat)
			require.Equal(t, mode, info.RelayMode)
			require.Equal(t, "/v1/images/edits", info.RequestURLPath)
			for _, badHost := range []string{"api.apib.ai.evil.example", "other.example"} {
				info.ChannelBaseUrl = "https://" + badHost
				require.False(t, IsAPIMartGeminiImage(info))
			}
			info.ChannelBaseUrl = "https://" + host
			info.OriginModelName = "grok-4-image"
			require.False(t, IsAPIMartGeminiImage(info))
		}
	}
}

func TestAPIMartGeminiRejectsUnsupportedParameters(t *testing.T) {
	for _, tc := range []struct {
		name    string
		request dto.ImageRequest
		mode    int
	}{
		{"format", dto.ImageRequest{ResponseFormat: "invalid"}, relayconstant.RelayModeImagesGenerations},
		{"multiple", dto.ImageRequest{N: common.GetPointer(uint(2))}, relayconstant.RelayModeImagesGenerations},
		{"mask", dto.ImageRequest{MaskUrl: "https://example.com/mask.png"}, relayconstant.RelayModeImagesGenerations},
		{"too-many-references", dto.ImageRequest{ImageUrls: strings.Split(strings.Repeat("https://example.com/ref.png,", 14)+"https://example.com/ref.png", ",")}, relayconstant.RelayModeImagesGenerations},
		{"missing-reference", dto.ImageRequest{}, relayconstant.RelayModeImagesEdits},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", "/v1/images/generations", strings.NewReader("{}"))
			c.Request.Header.Set("Content-Type", "application/json")
			info := &relaycommon.RelayInfo{OriginModelName: "gemini-3-pro-image", RelayMode: tc.mode, ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI, ChannelBaseUrl: "https://api.apib.ai"}}
			_, err := (&Adaptor{}).ConvertImageRequest(c, info, tc.request)
			require.Error(t, err)
			apiErr, ok := err.(*types.NewAPIError)
			require.True(t, ok)
			require.Equal(t, 400, apiErr.StatusCode)
			require.True(t, types.IsSkipRetryError(apiErr))
		})
	}
}
