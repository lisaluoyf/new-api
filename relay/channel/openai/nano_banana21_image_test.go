package openai

import (
	"bytes"
	"mime/multipart"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestNanoBanana21ImageBridge(t *testing.T) {
	for _, tier := range []string{"1K", "2K", "4K"} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("POST", "/v1/images/edits", strings.NewReader("{}"))
		c.Request.Header.Set("Content-Type", "application/json")
		info := &relaycommon.RelayInfo{OriginModelName: "gemini-nano-banana-2.1", RelayMode: relayconstant.RelayModeImagesEdits, PriceData: types.PriceData{}}
		req := dto.ImageRequest{Model: "google/gemini-nano-banana-2.1", Prompt: "change background", Resolution: tier, Size: "16:9", ImageUrls: []string{"https://example.com/reference.png"}, ResponseFormat: "b64_json"}
		wire, err := convertNanoBanana21ImageRequest(c, info, req)
		require.NoError(t, err)
		body := wire.(map[string]any)
		require.Equal(t, tier, body["image_config"].(map[string]string)["image_size"])
		require.Equal(t, "16:9", body["image_config"].(map[string]string)["aspect_ratio"])
		require.Equal(t, "google/gemini-nano-banana-2.1", body["model"])
		message := body["messages"].([]any)[0].(map[string]any)
		require.Len(t, message["content"], 2)
		raw := []byte(`{"choices":[{"message":{"images":[{"image_url":{"url":"data:image/png;base64,aGVsbG8="}}]}}],"usage":{"prompt_tokens":17,"completion_tokens":100}}`)
		normalized, err := normalizeNanoBanana21ImageResponse(c, info, raw)
		require.NoError(t, err)
		var parsed map[string]any
		require.NoError(t, common.Unmarshal(normalized, &parsed))
		require.Len(t, parsed["data"], 1)
		require.Equal(t, 1., info.PriceData.OtherRatios["n"])
		require.Equal(t, 1, service.ImageRequestDataFromContext(c)["actual_image_count"])
		require.Equal(t, "b64_json", service.GptImage2ClientResponseFormat(c))
		_, err = normalizeNanoBanana21ImageResponse(c, info, []byte(`{"choices":[{"message":{"content":"No image"}}]}`))
		require.Error(t, err)
		req.N = common.GetPointer(uint(2))
		_, err = convertNanoBanana21ImageRequest(c, info, req)
		require.Error(t, err)
	}
}

func TestNanoBanana21NativeImages(t *testing.T) {
	for _, mode := range []int{relayconstant.RelayModeImagesGenerations, relayconstant.RelayModeImagesEdits} {
		info := &relaycommon.RelayInfo{OriginModelName: "gemini-nano-banana-2.1", RelayMode: mode,
			ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI, UpstreamModelName: "nano-banana-2.1", ChannelBaseUrl: "https://api.a6api.com"}, PriceData: types.PriceData{}}
		require.True(t, nanoBanana21NativeImages(info))
		require.False(t, nanoBanana21ImageBridge(info))
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("POST", "/v1/images/edits", strings.NewReader("{}"))
		c.Request.Header.Set("Content-Type", "application/json")
		req := dto.ImageRequest{Model: "nano-banana-2.1", Prompt: "change background", Resolution: "1K", Size: "1024x1024", ResponseFormat: "b64_json"}
		path := "/v1/images/generations"
		if mode == relayconstant.RelayModeImagesEdits {
			path = "/v1/images/edits"
			req.ImageUrls = []string{"https://example.com/reference.png"}
		}
		url, err := (&Adaptor{}).GetRequestURL(info)
		require.NoError(t, err)
		require.Equal(t, "https://api.a6api.com"+path, url)
		wire, err := (&Adaptor{}).ConvertImageRequest(c, info, req)
		require.NoError(t, err)
		body := wire.(map[string]any)
		require.Equal(t, "nano-banana-2.1", body["model"])
		require.Equal(t, "1K", body["resolution"])
		require.NotContains(t, body, "messages")
		require.NotContains(t, body, "image_urls")
		if mode == relayconstant.RelayModeImagesEdits {
			require.Equal(t, req.ImageUrls[0], body["image"])
		}
		normalized, err := normalizeNanoBanana21ImageResponse(c, info, []byte(`{"data":[{"b64_json":"aGVsbG8="}]}`))
		require.NoError(t, err)
		var response map[string]any
		require.NoError(t, common.Unmarshal(normalized, &response))
		require.Len(t, response["data"], 1)
		require.Equal(t, 1., info.PriceData.OtherRatios["n"])
		require.Equal(t, 1, service.ImageRequestDataFromContext(c)["actual_image_count"])
		require.Equal(t, "b64_json", service.GptImage2ClientResponseFormat(c))
		_, err = normalizeNanoBanana21ImageResponse(c, info, []byte(`{"data":[]}`))
		require.Error(t, err)
		if mode == relayconstant.RelayModeImagesGenerations {
			req.ImageUrls = []string{"https://example.com/reference.png"}
			_, err = (&Adaptor{}).ConvertImageRequest(c, info, req)
			require.ErrorContains(t, err, "/v1/images/edits")
		}
		info.ChannelType = constant.ChannelTypeOpenRouter
		require.False(t, nanoBanana21NativeImages(info))
		require.True(t, nanoBanana21ImageBridge(info))
	}
}

func TestNanoBanana21NativeMultipartReference(t *testing.T) {
	oldCache := nanoBanana21CacheReference
	t.Cleanup(func() { nanoBanana21CacheReference = oldCache })
	nanoBanana21CacheReference = func(ref string) string {
		require.Equal(t, "data:image/png;base64,cmVmZXJlbmNl", ref)
		return "https://apimaster.ai/imgs/reference.png"
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	require.NoError(t, writer.WriteField("model", "gemini-nano-banana-2.1"))
	require.NoError(t, writer.WriteField("prompt", "change background"))
	file, err := writer.CreateFormFile("image", "reference.png")
	require.NoError(t, err)
	_, err = file.Write([]byte("reference"))
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/images/edits", &body)
	c.Request.Header.Set("Content-Type", writer.FormDataContentType())
	info := &relaycommon.RelayInfo{OriginModelName: "gemini-nano-banana-2.1", RelayMode: relayconstant.RelayModeImagesEdits,
		ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI, UpstreamModelName: "nano-banana-2.1"}}
	wire, err := convertNanoBanana21ImageRequest(c, info, dto.ImageRequest{Model: "nano-banana-2.1", ResponseFormat: "b64_json"})
	require.NoError(t, err)
	require.Equal(t, "nano-banana-2.1", wire.(map[string]any)["model"])
	require.Equal(t, "https://apimaster.ai/imgs/reference.png", wire.(map[string]any)["image"])
	_, err = normalizeNanoBanana21ImageResponse(c, info, []byte(`{"data":[{"b64_json":"aGVsbG8="}]}`))
	require.NoError(t, err)
	require.Equal(t, "b64_json", service.GptImage2ClientResponseFormat(c))
}
