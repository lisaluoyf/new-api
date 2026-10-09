package gemini

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"mime/multipart"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestGeminiImageEditsPassReferencesAndPreserveClientFormat(t *testing.T) {
	var pngData bytes.Buffer
	require.NoError(t, png.Encode(&pngData, image.NewRGBA(image.Rect(0, 0, 2, 2))))
	for _, multipartBody := range []bool{false, true} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("POST", "/v1/images/edits", nil)
		req := dto.ImageRequest{Model: "gemini-nano-banana-2.1", Prompt: "change background", Resolution: "2K", Size: "1024x1024", ResponseFormat: "b64_json"}
		if multipartBody {
			var body bytes.Buffer
			writer := multipart.NewWriter(&body)
			for k, v := range map[string]string{"prompt": req.Prompt, "resolution": "2K", "size": req.Size, "response_format": "b64_json"} {
				require.NoError(t, writer.WriteField(k, v))
			}
			f, err := writer.CreateFormFile("image", "ref.png")
			require.NoError(t, err)
			_, err = f.Write(pngData.Bytes())
			require.NoError(t, err)
			require.NoError(t, writer.Close())
			c.Request = httptest.NewRequest("POST", "/v1/images/edits", bytes.NewReader(body.Bytes()))
			c.Request.Header.Set("Content-Type", writer.FormDataContentType())
			t.Cleanup(func() { common.CleanupBodyStorage(c) })
		} else {
			req.Image, _ = common.Marshal("data:image/png;base64," + base64.StdEncoding.EncodeToString(pngData.Bytes()))
		}
		info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeImagesEdits, OriginModelName: req.Model,
			ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gemini-nano-banana-2.1-b"}}
		result, err := (&Adaptor{}).ConvertImageRequest(c, info, req)
		require.NoError(t, err)
		converted := result.(*dto.GeminiChatRequest)
		require.Len(t, converted.Contents[0].Parts, 2)
		require.Equal(t, base64.StdEncoding.EncodeToString(pngData.Bytes()), converted.Contents[0].Parts[1].InlineData.Data)
		require.Equal(t, "image/png", converted.Contents[0].Parts[1].InlineData.MimeType)
		require.Equal(t, "b64_json", c.GetString("gpt_image2_client_response_format"))
		require.Equal(t, "2K", service.ImageRequestDataFromContext(c)["effective_resolution"])
		var config map[string]string
		require.NoError(t, common.Unmarshal(converted.GenerationConfig.ImageConfig, &config))
		require.Equal(t, "2K", config["imageSize"])
		require.Equal(t, "1:1", config["aspectRatio"])
	}
}

func TestGeminiMappedImageGenerationPreservesAliasAndResolution(t *testing.T) {
	for _, tier := range []string{"1K", "2K", "4K"} {
		t.Run(tier, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", "/v1/images/generations", nil)
			req := dto.ImageRequest{Model: "gemini-nano-banana-2.1", Prompt: "teapot", Resolution: tier, Size: "1:1"}
			info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeImagesGenerations, OriginModelName: req.Model,
				ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gemini-nano-banana-2.1-b", ChannelBaseUrl: "https://upstream.example"}}
			adaptor := &Adaptor{}
			result, err := adaptor.ConvertImageRequest(c, info, req)
			require.NoError(t, err)
			converted := result.(*dto.GeminiChatRequest)
			require.Equal(t, []string{"TEXT", "IMAGE"}, converted.GenerationConfig.ResponseModalities)
			var config map[string]string
			require.NoError(t, common.Unmarshal(converted.GenerationConfig.ImageConfig, &config))
			require.Equal(t, tier, config["imageSize"])
			require.Equal(t, "1:1", config["aspectRatio"])
			url, err := adaptor.GetRequestURL(info)
			require.NoError(t, err)
			require.Equal(t, "https://upstream.example/v1beta/models/gemini-nano-banana-2.1-b:generateContent", url)
			require.Equal(t, req.Model, info.OriginModelName)
		})
	}
}

func TestGeminiImageRejectsUnsupportedParameters(t *testing.T) {
	for _, req := range []dto.ImageRequest{
		{N: common.GetPointer(uint(2))}, {Resolution: "8K"}, {Size: "0x1024"},
		{ResponseFormat: "bad"}, {MaskUrl: "https://example.test/mask.png"},
		{Extra: map[string]json.RawMessage{"stream": []byte("true")}},
	} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("POST", "/v1/images/generations", nil)
		_, err := convertGeminiImagineImageRequest(c, &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeImagesGenerations}, req)
		require.Error(t, err)
	}
}

func TestGeminiImageBillingCountsActualImagesExcludingThoughts(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	service.SetImageRequestDataOnContext(c, &dto.ImageRequest{Model: "gemini-nano-banana-2.1", Resolution: "4K"})
	info := &relaycommon.RelayInfo{}
	response := dto.GeminiChatResponse{Candidates: []dto.GeminiChatCandidate{{Content: dto.GeminiChatContent{Parts: []dto.GeminiPart{
		{InlineData: &dto.GeminiInlineData{MimeType: "image/png", Data: "a"}},
		{InlineData: &dto.GeminiInlineData{MimeType: "image/png", Data: "b"}},
		{Thought: true, InlineData: &dto.GeminiInlineData{MimeType: "image/png", Data: "thought"}},
		{Text: "no charge for text"},
	}}}}}
	require.NoError(t, recordGeminiImageOutput(c, info, &response))
	require.Equal(t, float64(2), info.PriceData.OtherRatios["n"])
	require.Equal(t, 2, service.ImageRequestDataFromContext(c)["actual_image_count"])
	require.Error(t, recordGeminiImageOutput(c, info, &dto.GeminiChatResponse{}))
}
