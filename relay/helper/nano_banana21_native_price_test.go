package helper

import (
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestNativeNanoImagePricingCanonicalizesWireAndRejectsUnsupportedRequests(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/models/gemini-nano-banana-2.1:generateContent", nil)
	req := &dto.GeminiChatRequest{}
	req.GenerationConfig.ImageConfig = []byte(`{"image_size":"2k","aspect_ratio":"16:9"}`)
	info := &relaycommon.RelayInfo{OriginModelName: "gemini-nano-banana-2.1", Request: req}
	meta := &types.TokenCountMeta{}
	require.NoError(t, nanoBanana21NativeImagePricing(c, info, meta))
	require.Equal(t, "2K", meta.ImagePriceVariant)
	var wire map[string]string
	require.NoError(t, common.Unmarshal(req.GenerationConfig.ImageConfig, &wire))
	require.Equal(t, "2K", wire["imageSize"])
	require.Equal(t, "16:9", wire["aspectRatio"])
	require.NotContains(t, wire, "image_size")
	for _, raw := range []string{`{"imageSize":"8K"}`, `{"imageSize":false}`} {
		req.GenerationConfig.ImageConfig = []byte(raw)
		require.Error(t, nanoBanana21NativeImagePricing(c, info, meta))
	}
	req.GenerationConfig.ImageConfig = nil
	req.GenerationConfig.CandidateCount = common.GetPointer(2)
	require.Error(t, nanoBanana21NativeImagePricing(c, info, meta))
	req.GenerationConfig.CandidateCount = nil
	c.Request = httptest.NewRequest("POST", "/v1/models/gemini-nano-banana-2.1:streamGenerateContent", nil)
	require.Error(t, nanoBanana21NativeImagePricing(c, info, meta))
}
