package openai

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relay/helper"
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

func TestNanoBanana21MarkdownImageResponses(t *testing.T) {
	for _, tc := range []struct {
		name    string
		content any
		images  []any
		want    []map[string]string
	}{
		{"subrouter jpeg", "![image](data:image/jpeg;base64,aGVsbG8=)", nil, []map[string]string{{"b64_json": "aGVsbG8="}}},
		{"png with surrounding text", "Here is your image:\n![result](<data:image/png;base64,aGVsbG8=>)\nDone.", nil, []map[string]string{{"b64_json": "aGVsbG8="}}},
		{"two outputs", "![one](https://example.com/a.png)\n![two](data:image/png;base64,aGVsbG8=)", nil, []map[string]string{{"url": "https://example.com/a.png"}, {"b64_json": "aGVsbG8="}}},
		{"duplicate markup", "![one](https://example.com/a.png)\n![again](https://example.com/a.png)", nil, []map[string]string{{"url": "https://example.com/a.png"}}},
		{"typed text content", []any{map[string]any{"type": "text", "text": "![image](data:image/png;base64,aGVsbG8=)"}}, nil, []map[string]string{{"b64_json": "aGVsbG8="}}},
		{"structured wins", "![image](https://example.com/a.png)", []any{map[string]any{"image_url": map[string]string{"url": "https://example.com/a.png"}}}, []map[string]string{{"url": "https://example.com/a.png"}}},
		{"text only", "I cannot generate an image.", nil, nil},
		{"ordinary link", "[image](https://example.com/a.png)", nil, nil},
		{"bare data URI", "data:image/png;base64,aGVsbG8=", nil, nil},
		{"non image data", "![file](data:text/plain;base64,aGVsbG8=)", nil, nil},
		{"invalid base64", "![image](data:image/png;base64,abc=def=)", nil, nil},
		{"empty payload", "![image](data:image/png;base64,)", nil, nil},
		{"unsupported scheme", "![image](file:///tmp/image.png)", nil, nil},
		{"null content", nil, nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", "/v1/images/generations", strings.NewReader("{}"))
			info := &relaycommon.RelayInfo{OriginModelName: "gemini-nano-banana-2.1", RelayMode: relayconstant.RelayModeImagesGenerations, PriceData: types.PriceData{}}
			_, err := convertNanoBanana21ImageRequest(c, info, dto.ImageRequest{Model: info.OriginModelName, Resolution: "1K", ResponseFormat: "b64_json"})
			require.NoError(t, err)
			message := map[string]any{"content": tc.content}
			if tc.images != nil {
				message["images"] = tc.images
			}
			raw, err := common.Marshal(map[string]any{"choices": []any{map[string]any{"message": message}}, "usage": map[string]any{"prompt_tokens": 17, "completion_tokens": 100}})
			require.NoError(t, err)
			body, err := normalizeNanoBanana21ImageResponse(c, info, raw)
			if tc.want == nil {
				require.Error(t, err)
				require.NotContains(t, info.PriceData.OtherRatios, "n")
				return
			}
			require.NoError(t, err)
			var response struct {
				Data  []map[string]string `json:"data"`
				Usage dto.Usage           `json:"usage"`
			}
			require.NoError(t, common.Unmarshal(body, &response))
			require.Equal(t, tc.want, response.Data)
			require.Equal(t, 17, response.Usage.PromptTokens)
			require.Equal(t, 100, response.Usage.CompletionTokens)
			require.Equal(t, float64(len(tc.want)), info.PriceData.OtherRatios["n"])
			require.Equal(t, len(tc.want), service.ImageRequestDataFromContext(c)["actual_image_count"])
			require.Equal(t, "b64_json", service.GptImage2ClientResponseFormat(c))
		})
	}
}

func TestNanoBanana21SubrouterNativeGemini(t *testing.T) {
	for _, tier := range []string{"1K", "2K", "4K"} {
		edge := map[string]int{"1K": 1024, "2K": 2048, "4K": 4096}[tier]
		var generated bytes.Buffer
		require.NoError(t, png.Encode(&generated, image.NewGray(image.Rect(0, 0, edge, edge))))
		payload := base64.StdEncoding.EncodeToString(generated.Bytes())
		for _, mode := range []int{relayconstant.RelayModeImagesGenerations, relayconstant.RelayModeImagesEdits} {
			info := &relaycommon.RelayInfo{OriginModelName: "gemini-nano-banana-2.1", RelayMode: mode, ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI, ChannelBaseUrl: "https://subrouter.ai/v1", UpstreamModelName: "gemini-nano-banana-2.1"}, PriceData: types.PriceData{}}
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", "/v1/images/generations", strings.NewReader("{}"))
			c.Request.Header.Set("Content-Type", "application/json")
			req := dto.ImageRequest{Model: info.OriginModelName, Prompt: "background only", Resolution: tier, Size: "16:9", ResponseFormat: "b64_json"}
			if mode == relayconstant.RelayModeImagesEdits {
				req.ImageUrls = []string{"data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jE9sAAAAASUVORK5CYII="}
			}
			wire, err := (&Adaptor{}).ConvertImageRequest(c, info, req)
			require.NoError(t, err)
			body := wire.(map[string]any)
			config := body["generationConfig"].(map[string]any)
			require.Equal(t, tier, config["imageConfig"].(map[string]string)["imageSize"])
			require.Equal(t, "16:9", config["imageConfig"].(map[string]string)["aspectRatio"])
			parts := body["contents"].([]any)[0].(map[string]any)["parts"].([]any)
			require.Equal(t, "background only", parts[0].(map[string]any)["text"])
			if mode == relayconstant.RelayModeImagesEdits {
				require.Len(t, parts, 2)
				require.Equal(t, "image/png", parts[1].(map[string]any)["inlineData"].(map[string]string)["mimeType"])
			}
			upstreamURL, err := (&Adaptor{}).GetRequestURL(info)
			require.NoError(t, err)
			require.Equal(t, "https://subrouter.ai/v1beta/models/gemini-nano-banana-2.1:generateContent", upstreamURL)
			raw := []byte(`{"candidates":[{"content":{"parts":[{"thought":true,"inlineData":{"mimeType":"image/png","data":"dGhvdWdodA=="}},{"text":"done"},{"inlineData":{"mimeType":"image/png","data":"` + payload + `"}}]}}],"usageMetadata":{"promptTokenCount":17,"candidatesTokenCount":100,"totalTokenCount":117}}`)
			out, err := normalizeNanoBanana21ImageResponse(c, info, raw)
			require.NoError(t, err)
			var response struct {
				Data  []map[string]string `json:"data"`
				Usage dto.Usage           `json:"usage"`
			}
			require.NoError(t, common.Unmarshal(out, &response))
			require.Equal(t, []map[string]string{{"b64_json": payload}}, response.Data)
			require.Equal(t, 117, response.Usage.TotalTokens)
			require.Equal(t, 1., info.PriceData.OtherRatios["n"])
			_, err = normalizeNanoBanana21ImageResponse(c, info, []byte(`{"candidates":[{"content":{"parts":[{"text":"no image"}]}}]}`))
			require.Error(t, err)
			_, err = normalizeNanoBanana21ImageResponse(c, info, []byte(`{"candidates":[{"content":{"parts":[{"inlineData":{"mimeType":"image/png","data":"iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jE9sAAAAASUVORK5CYII="}}]}}]}`))
			require.ErrorContains(t, err, "smaller than requested")
		}
	}
}

func TestNanoBanana21ValidationIsClientError(t *testing.T) {
	for _, raw := range []string{
		`{"model":"gemini-nano-banana-2.1","prompt":"scene","stream":true}`,
		`{"model":"gemini-nano-banana-2.1","prompt":"scene","mask_url":"https://example.com/mask.png"}`,
		`{"model":"gemini-nano-banana-2.1","prompt":"scene","size":"3:7"}`,
		`{"model":"gemini-nano-banana-2.1","prompt":"scene","response_format":"invalid"}`,
	} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("POST", "/v1/images/generations", strings.NewReader(raw))
		var request dto.ImageRequest
		require.NoError(t, common.Unmarshal([]byte(raw), &request))
		info := &relaycommon.RelayInfo{OriginModelName: request.Model, RelayMode: relayconstant.RelayModeImagesGenerations, PriceData: types.PriceData{}}
		_, err := (&Adaptor{}).ConvertImageRequest(c, info, request)
		require.Error(t, err)
		var apiErr *types.NewAPIError
		require.ErrorAs(t, err, &apiErr)
		require.Equal(t, http.StatusBadRequest, apiErr.StatusCode)
		require.True(t, types.IsSkipRetryError(apiErr))
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
		require.Equal(t, "1K", body["quality"])
		require.NotContains(t, body, "messages")
		require.NotContains(t, body, "image_urls")
		if mode == relayconstant.RelayModeImagesEdits {
			require.Equal(t, req.ImageUrls[0], body["image"])
			for _, tier := range []string{"2K", "4K"} {
				req.Resolution = tier
				wire, err := (&Adaptor{}).ConvertImageRequest(c, info, req)
				require.NoError(t, err)
				require.Equal(t, tier, wire.(map[string]any)["quality"])
			}
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
			wire, err = (&Adaptor{}).ConvertImageRequest(c, info, req)
			require.NoError(t, err)
			require.Equal(t, req.ImageUrls[0], wire.(map[string]any)["image"])
			require.True(t, c.GetBool("nano_banana21_native_edit"))
		}
		info.ChannelType = constant.ChannelTypeOpenRouter
		require.False(t, nanoBanana21NativeImages(info))
		require.True(t, nanoBanana21ImageBridge(info))
	}
}

func TestNanoBanana21NativeMultipartReference(t *testing.T) {
	service.InitHttpClient()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/images/edits", r.URL.Path)
		require.Equal(t, "application/json", r.Header.Get("Content-Type"))
		var body map[string]any
		require.NoError(t, common.DecodeJson(r.Body, &body))
		require.Equal(t, "https://apimaster.ai/imgs/reference.png", body["image"])
		_, _ = io.WriteString(w, `{}`)
	}))
	defer upstream.Close()
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
	require.NoError(t, writer.WriteField("response_format", "b64_json"))
	file, err := writer.CreateFormFile("image", "reference.png")
	require.NoError(t, err)
	_, err = file.Write([]byte("reference"))
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/images/edits", &body)
	c.Request.Header.Set("Content-Type", writer.FormDataContentType())
	info := &relaycommon.RelayInfo{OriginModelName: "gemini-nano-banana-2.1", RelayMode: relayconstant.RelayModeImagesEdits,
		ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI, UpstreamModelName: "nano-banana-2.1", ChannelBaseUrl: upstream.URL}}
	req, err := helper.GetAndValidOpenAIImageRequest(c, relayconstant.RelayModeImagesEdits)
	require.NoError(t, err)
	require.Equal(t, "b64_json", req.ResponseFormat)
	req.Model = "nano-banana-2.1"
	wire, err := convertNanoBanana21ImageRequest(c, info, *req)
	require.NoError(t, err)
	require.Equal(t, "nano-banana-2.1", wire.(map[string]any)["model"])
	require.Equal(t, "https://apimaster.ai/imgs/reference.png", wire.(map[string]any)["image"])
	raw, err := common.Marshal(wire)
	require.NoError(t, err)
	a := &Adaptor{}
	a.Init(info)
	resp, err := a.DoRequest(c, info, bytes.NewReader(raw))
	require.NoError(t, err)
	resp.(*http.Response).Body.Close()
	require.Equal(t, writer.FormDataContentType(), c.GetHeader("Content-Type"))
	_, err = normalizeNanoBanana21ImageResponse(c, info, []byte(`{"data":[{"b64_json":"aGVsbG8="}]}`))
	require.NoError(t, err)
	require.Equal(t, "b64_json", service.GptImage2ClientResponseFormat(c))
}

func TestNanoBanana21GenerationReferencesUseAttemptLocalEdit(t *testing.T) {
	service.InitHttpClient()
	oldCache := nanoBanana21CacheReference
	t.Cleanup(func() { nanoBanana21CacheReference = oldCache })
	nanoBanana21CacheReference = func(string) string { return "https://example.com/cached.png" }
	for _, field := range []string{"image_urls", "image", "images"} {
		t.Run(field, func(t *testing.T) {
			refs := []string{"https://example.com/a.png", "https://example.com/b.png", "https://example.com/c.png", "https://example.com/d.png", "https://example.com/e.png", "data:image/png;base64,cmVm"}
			var path string
			var received map[string]any
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				path = r.URL.Path
				require.Equal(t, "application/json", r.Header.Get("Content-Type"))
				received = nil
				require.NoError(t, common.DecodeJson(r.Body, &received))
				_, _ = io.WriteString(w, `{}`)
			}))
			defer upstream.Close()
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", "/v1/images/generations", strings.NewReader("{}"))
			c.Request.Header.Set("Content-Type", "application/json")
			info := &relaycommon.RelayInfo{OriginModelName: "gemini-nano-banana-2.1", RelayMode: relayconstant.RelayModeImagesGenerations, RequestURLPath: c.Request.URL.Path,
				ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI, UpstreamModelName: "nano-banana-2.1", ChannelBaseUrl: upstream.URL}}
			payload, err := common.Marshal(map[string]any{"model": "nano-banana-2.1", "prompt": "preserve all reference objects", "resolution": "1K", field: refs})
			require.NoError(t, err)
			var req dto.ImageRequest
			require.NoError(t, common.Unmarshal(payload, &req))
			a := &Adaptor{}
			a.Init(info)
			wire, err := a.ConvertImageRequest(c, info, req)
			require.NoError(t, err)
			raw, err := common.Marshal(wire)
			require.NoError(t, err)
			resp, err := a.DoRequest(c, info, bytes.NewReader(raw))
			require.NoError(t, err)
			resp.(*http.Response).Body.Close()
			require.Equal(t, "/v1/images/edits", path)
			refs[5] = "https://example.com/cached.png"
			var got []string
			encoded, err := common.Marshal(received["images"])
			require.NoError(t, err)
			require.NoError(t, common.Unmarshal(encoded, &got))
			require.Equal(t, refs, got)
			require.NotContains(t, received, "image_urls")
			require.Equal(t, relayconstant.RelayModeImagesGenerations, info.RelayMode)
			require.Equal(t, "/v1/images/generations", info.RequestURLPath)
			require.Equal(t, info.RequestURLPath, c.Request.URL.Path)
			// A native text-only retry must still use generations.
			wire, err = a.ConvertImageRequest(c, info, dto.ImageRequest{Model: "nano-banana-2.1", Prompt: "no references"})
			require.NoError(t, err)
			raw, err = common.Marshal(wire)
			require.NoError(t, err)
			resp, err = a.DoRequest(c, info, bytes.NewReader(raw))
			require.NoError(t, err)
			resp.(*http.Response).Body.Close()
			require.Equal(t, "/v1/images/generations", path)
			require.NotContains(t, received, "images")
		})
	}
}
