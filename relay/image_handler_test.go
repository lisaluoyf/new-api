package relay

import (
	"bytes"
	"fmt"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/helper"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
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

func TestImageHelperSeedreamInvalidParameter(t *testing.T) {
	service.InitHttpClient()
	for _, tc := range []struct {
		name, model, code string
		status            int
		skipRetry         bool
	}{
		{"invalid parameters", dto.Seedream5ProModel, "InvalidParameter", 400, true},
		{"upstream failure", dto.Seedream5ProModel, "InternalError", 500, false},
		{"rate limit", dto.Seedream5ProModel, "TooManyRequests", 429, false},
		{"other model", "doubao-seedream-4-5-251128", "InvalidParameter", 400, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				fmt.Fprintf(w, `{"error":{"code":%q,"message":"test upstream error","type":"invalid_request_error"}}`, tc.code)
			}))
			defer upstream.Close()
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)
			common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeVolcEngine)
			common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, upstream.URL)
			common.SetContextKey(c, constant.ContextKeyOriginalModel, tc.model)
			info := &relaycommon.RelayInfo{
				OriginModelName: tc.model,
				RelayMode:       relayconstant.RelayModeImagesGenerations,
				Request:         &dto.ImageRequest{Model: tc.model, Prompt: "test", Size: "1K"},
			}
			err := ImageHelper(c, info)
			require.NotNil(t, err)
			require.Equal(t, tc.status, err.StatusCode)
			require.Equal(t, types.ErrorCode(tc.code), err.GetErrorCode())
			require.Contains(t, err.Error(), "test upstream error")
			require.Equal(t, tc.skipRetry, types.IsSkipRetryError(err))
		})
	}
}

// Exercise two converted retries followed by a native multipart retry through
// the real ImageHelper, including pass-through-enabled channel settings.
func TestImageEditsGenerationFallbackPreservesMultipart(t *testing.T) {
	service.InitHttpClient()
	for _, path := range []string{"/v1/images/edits", "/v1/images/edits/async"} {
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		for k, v := range map[string]string{"model": "gpt-image-2", "prompt": "edit", "resolution": "2k", "size": "1:1"} {
			require.NoError(t, writer.WriteField(k, v))
		}
		part, err := writer.CreateFormFile("image", "ref.png")
		require.NoError(t, err)
		_, err = part.Write([]byte("original-image"))
		require.NoError(t, err)
		require.NoError(t, writer.Close())
		ct := writer.FormDataContentType()
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("POST", path, bytes.NewReader(body.Bytes()))
		c.Request.Header.Set("Content-Type", ct)
		t.Cleanup(func() { common.CleanupBodyStorage(c) })
		request, err := helper.GetAndValidOpenAIImageRequest(c, relayconstant.RelayModeImagesEdits)
		require.NoError(t, err)
		require.Equal(t, "2k", request.Resolution)
		info := &relaycommon.RelayInfo{OriginModelName: "gpt-image-2", RelayMode: relayconstant.RelayModeImagesEdits, RequestURLPath: path, Request: request}
		common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeOpenAI)
		common.SetContextKey(c, constant.ContextKeyOriginalModel, "gpt-image-2")
		common.SetContextKey(c, constant.ContextKeyChannelOtherSetting, dto.ChannelOtherSettings{GptImage2Capabilities: &dto.GptImage2Capabilities{SizeFormat: dto.GptImage2SizeFormatAspectRatioWithResolution}})
		for _, id := range []int{81, 59, 149, 102} {
			var gotPath, gotType string
			var raw []byte
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				gotType = r.Header.Get("Content-Type")
				raw, _ = io.ReadAll(r.Body)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(503)
				_, _ = io.WriteString(w, `{"error":{"message":"test retry","type":"upstream_error"}}`)
			}))
			common.SetContextKey(c, constant.ContextKeyChannelId, id)
			common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, upstream.URL)
			common.SetContextKey(c, constant.ContextKeyChannelSetting, dto.ChannelSettings{PassThroughBodyEnabled: id == 81 || id == 59})
			relayErr := ImageHelper(c, info)
			upstream.Close()
			require.NotNil(t, relayErr)
			require.Equal(t, 503, relayErr.StatusCode)
			require.Equal(t, ct, c.GetHeader("Content-Type"))
			require.Equal(t, path, c.Request.URL.Path)
			if id == 102 || id == 149 {
				require.Equal(t, "/v1/images/edits", gotPath)
				require.Contains(t, gotType, "multipart/form-data")
				require.Contains(t, string(raw), "original-image")
			} else {
				require.Equal(t, "/v1/images/generations", gotPath)
				require.Equal(t, "application/json", gotType)
				var converted dto.ImageRequest
				require.NoError(t, common.Unmarshal(raw, &converted))
				require.Len(t, converted.ImageUrls, 1)
				require.Equal(t, "2k", converted.Resolution)
				filter := service.GptImage2ChannelPickFilter(c, "gpt-image-2")
				ch := &model.Channel{Id: 59}
				ch.SetOtherSettings(dto.ChannelOtherSettings{GptImage2Capabilities: &dto.GptImage2Capabilities{Version: 1, Enabled: true, Generations: &dto.GptImage2EndpointCapabilities{Enabled: true, MaxN: 1, MaxImageURLs: 16, OptionalFields: []string{"size", "resolution"}}}})
				require.True(t, filter(ch), "fallback must still recognize the original multipart body")
			}
		}
	}
}

// Inspect the actual upstream request after pre-routing normalization and relay,
// including a retry to the other native-edits channel.
func TestImage25NativeEditsPreservesReferenceAcrossRetries(t *testing.T) {
	service.InitHttpClient()
	for _, path := range []string{"/v1/images/edits", "/v1/images/edits/async"} {
		for _, name := range []string{"gpt-image-2.5-sunburst", "gpt-image-2.5-flare"} {
			for _, field := range []string{"image", "image[]", "images"} {
				t.Run(path+name+field, func(t *testing.T) {
					var body bytes.Buffer
					writer := multipart.NewWriter(&body)
					for k, v := range map[string]string{"model": name, "prompt": "change background only", "size": "1024x1024", "n": "1", "output_compression": "0", "watermark": "false"} {
						require.NoError(t, writer.WriteField(k, v))
					}
					for i := 0; i < 3; i++ {
						part, err := writer.CreateFormFile(field, fmt.Sprintf("reference-%d.png", i))
						require.NoError(t, err)
						_, err = part.Write([]byte(fmt.Sprintf("unique-reference-%d-bytes", i)))
						require.NoError(t, err)
					}
					require.NoError(t, writer.Close())
					ct := writer.FormDataContentType()
					c, _ := gin.CreateTestContext(httptest.NewRecorder())
					c.Request = httptest.NewRequest("POST", path, bytes.NewReader(body.Bytes()))
					c.Request.Header.Set("Content-Type", ct)
					t.Cleanup(func() { common.CleanupBodyStorage(c) })
					require.NoError(t, helper.NormalizeGptImage25ReferenceRequest(c, name))
					require.Equal(t, name, service.PrepareGptImage2ModelRequest(c, name))
					request, err := helper.GetAndValidOpenAIImageRequest(c, relayconstant.RelayModeImagesEdits)
					require.NoError(t, err)
					info := &relaycommon.RelayInfo{OriginModelName: name, RelayMode: relayconstant.RelayModeImagesEdits, RequestURLPath: c.Request.URL.Path, Request: request}
					common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeOpenAI)
					common.SetContextKey(c, constant.ContextKeyOriginalModel, name)
					for _, id := range []int{73, 102} {
						caps := &dto.GptImage2Capabilities{Version: 1, Enabled: true, SizeFormat: dto.GptImage2SizeFormatPixelDimensions, Edits: &dto.GptImage2EndpointCapabilities{Enabled: true, Multipart: true, UploadedImage: true, RequireUploadedImage: true, MaxN: 1, OptionalFields: []string{"*"}}}
						ch := &model.Channel{Id: id}
						ch.SetOtherSettings(dto.ChannelOtherSettings{GptImage2Capabilities: caps})
						require.True(t, service.GptImage2ChannelPickFilter(c, name)(ch))
						caps.Edits.Enabled = false
						ch.SetOtherSettings(dto.ChannelOtherSettings{GptImage2Capabilities: caps})
						require.False(t, service.GptImage2ChannelPickFilter(c, name)(ch), "disabled edits must not route through generations")
						caps.Edits.Enabled = true
						var gotPath, gotType string
						var raw []byte
						upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
							gotPath = r.URL.Path
							gotType = r.Header.Get("Content-Type")
							raw, _ = io.ReadAll(r.Body)
							w.Header().Set("Content-Type", "application/json")
							w.WriteHeader(503)
							_, _ = io.WriteString(w, `{"error":{"message":"test retry","type":"upstream_error"}}`)
						}))
						common.SetContextKey(c, constant.ContextKeyChannelId, id)
						common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, upstream.URL)
						common.SetContextKey(c, constant.ContextKeyChannelOtherSetting, dto.ChannelOtherSettings{GptImage2Capabilities: caps})
						common.SetContextKey(c, constant.ContextKeyChannelSetting, dto.ChannelSettings{PassThroughBodyEnabled: id == 102})
						relayErr := ImageHelper(c, info)
						upstream.Close()
						require.NotNil(t, relayErr)
						require.Equal(t, 503, relayErr.StatusCode)
						require.Equal(t, "/v1/images/edits", gotPath)
						require.Contains(t, gotType, "multipart/form-data")
						received := httptest.NewRequest("POST", gotPath, bytes.NewReader(raw))
						received.Header.Set("Content-Type", gotType)
						require.NoError(t, received.ParseMultipartForm(1<<20))
						require.Equal(t, name, received.FormValue("model"))
						require.Equal(t, "0", received.FormValue("output_compression"))
						require.Equal(t, "false", received.FormValue("watermark"))
						files := received.MultipartForm.File["image"]
						if len(files) == 0 {
							files = received.MultipartForm.File["image[]"]
						}
						if len(files) == 0 {
							files = received.MultipartForm.File["images"]
						}
						require.Len(t, files, 3)
						for i, file := range files {
							f, err := file.Open()
							require.NoError(t, err)
							actual, err := io.ReadAll(f)
							f.Close()
							require.NoError(t, err)
							require.Equal(t, fmt.Sprintf("unique-reference-%d-bytes", i), string(actual))
						}
						require.NoError(t, received.MultipartForm.RemoveAll())
						require.Equal(t, ct, c.GetHeader("Content-Type"))
						require.NoError(t, helper.NormalizeGptImage25ReferenceRequest(c, name))
					}
				})
			}
		}
	}
}
