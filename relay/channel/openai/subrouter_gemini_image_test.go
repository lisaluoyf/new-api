package openai

import (
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestSubrouterGeminiImageWirePreservesReferencesAndResolution(t *testing.T) {
	for _, model := range []string{"gemini-3.1-flash-image", "gemini-3-pro-image-preview"} {
		for _, tier := range []struct{ resolution, size string }{{"1k", "1024x1024"}, {"2K", "2048x2048"}, {"4k", "4096x4096"}} {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", "/v1/images/generations/async", nil)
			c.Request.Header.Set("Content-Type", "application/json")
			info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeImagesGenerations, ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://subrouter.ai"}}
			refs := []string{"https://example.com/image.png?signature=a%2Fb&expires=123"}
			wire, err := (&Adaptor{}).ConvertImageRequest(c, info, dto.ImageRequest{Model: model, Prompt: "background only", Size: "1:1", Resolution: tier.resolution, ImageUrls: refs})
			require.NoError(t, err)
			raw, err := common.Marshal(wire)
			require.NoError(t, err)
			var sent dto.ImageRequest
			require.NoError(t, common.Unmarshal(raw, &sent))
			require.Equal(t, tier.size, sent.Size)
			require.Equal(t, tier.resolution, sent.Resolution)
			require.Equal(t, model, sent.Model)
			require.Equal(t, refs, sent.ImageUrls)
		}
	}
}

func TestSubrouterGeminiImageNormalizationScope(t *testing.T) {
	for _, test := range []struct{ base, model, size, want string }{
		{"https://subrouter.ai", "gemini-3.1-flash-image", "3:2", "1536x1024"},
		{"https://subrouter.ai", "gemini-3-pro-image-preview", "2:3", "1024x1536"},
		{"https://subrouter.ai", "gemini-3.1-flash-image", "2048x2048", "2048x2048"},
		{"https://subrouter.ai", "gemini-3.1-flash-image", "auto", "auto"},
		{"https://subrouter.ai", "gpt-image-2", "1:1", "1:1"},
		{"https://other.example", "gemini-3.1-flash-image", "1:1", "1:1"},
		{"https://subrouter.ai.other.example", "gemini-3.1-flash-image", "1:1", "1:1"},
	} {
		req := dto.ImageRequest{Model: test.model, Size: test.size}
		normalizeSubrouterGeminiImageRequest(&relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: test.base}}, &req)
		require.Equal(t, test.want, req.Size)
	}
}
