package openai

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
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
