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
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type apimartGeminiTransport func(*http.Request) (*http.Response, error)

func (f apimartGeminiTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestAPIMartGeminiFallbackPreservesReferenceAndReturnsCompletedBase64(t *testing.T) {
	service.InitHttpClient()
	client := service.GetHttpClient()
	old := client.Transport
	oldDefault := http.DefaultTransport
	t.Cleanup(func() { client.Transport = old; http.DefaultTransport = oldDefault })
	var pngBody bytes.Buffer
	require.NoError(t, png.Encode(&pngBody, image.NewRGBA(image.Rect(0, 0, 2, 2))))
	output := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(pngBody.Bytes())
	}))
	defer output.Close()
	// Allow only this local fixture's port during the download assertions.
	// Production SSRF settings are unchanged.
	fetch := system_setting.GetFetchSetting()
	oldFetch := *fetch
	fetch.AllowPrivateIp = true
	fetch.AllowedPorts = []string{strings.TrimPrefix(output.URL, "http://127.0.0.1:")}
	t.Cleanup(func() { *fetch = oldFetch })
	for _, multipartClient := range []bool{false, true} {
		t.Run(map[bool]string{false: "json", true: "multipart"}[multipartClient], func(t *testing.T) {
			var body bytes.Buffer
			ct := "application/json"
			if multipartClient {
				writer := multipart.NewWriter(&body)
				for k, v := range map[string]string{"model": "gemini-3-pro-image", "prompt": "edit reference", "size": "1:1", "resolution": "2K", "response_format": "b64_json"} {
					require.NoError(t, writer.WriteField(k, v))
				}
				part, err := writer.CreateFormFile("image", "original.png")
				require.NoError(t, err)
				_, err = part.Write(pngBody.Bytes())
				require.NoError(t, err)
				require.NoError(t, writer.Close())
				ct = writer.FormDataContentType()
			} else {
				raw, err := common.Marshal(dto.ImageRequest{Model: "gemini-3-pro-image", Prompt: "edit reference", ImageUrls: []string{"data:image/png;base64," + base64.StdEncoding.EncodeToString(pngBody.Bytes())}, Size: "1:1", Resolution: "2K", ResponseFormat: "b64_json"})
				require.NoError(t, err)
				body.Write(raw)
			}
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest("POST", "/v1/images/edits", bytes.NewReader(body.Bytes()))
			c.Request.Header.Set("Content-Type", ct)
			t.Cleanup(func() { common.CleanupBodyStorage(c) })
			req, err := helper.GetAndValidOpenAIImageRequest(c, relayconstant.RelayModeImagesEdits)
			require.NoError(t, err)
			polls := 0
			submits := 0
			client.Transport = apimartGeminiTransport(func(r *http.Request) (*http.Response, error) {
				if r.URL.Host != "api.apib.ai" {
					return oldDefault.RoundTrip(r)
				}
				require.Equal(t, "api.apib.ai", r.URL.Host)
				payload := ""
				if r.Method == "POST" {
					submits++
					require.Equal(t, "/v1/images/generations", r.URL.Path)
					require.Equal(t, "application/json", r.Header.Get("Content-Type"))
					var wire dto.ImageRequest
					require.NoError(t, common.DecodeJson(r.Body, &wire))
					require.Equal(t, "gemini-3-pro-image-preview", wire.Model)
					require.Equal(t, "url", wire.ResponseFormat)
					require.Equal(t, "2K", wire.Resolution)
					require.Len(t, wire.ImageUrls, 1)
					require.Equal(t, "data:image/png;base64,"+base64.StdEncoding.EncodeToString(pngBody.Bytes()), wire.ImageUrls[0])
					payload = `{"code":200,"data":[{"status":"submitted","task_id":"test-provider-task"}]}`
				} else {
					polls++
					require.Equal(t, "/v1/tasks/test-provider-task", r.URL.Path)
					raw, err := common.Marshal(map[string]any{"code": 200, "data": map[string]any{"status": "completed", "result": map[string]any{"images": []any{map[string]any{"url": []string{output.URL + "/result.png"}}}}}})
					require.NoError(t, err)
					payload = string(raw)
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(payload)), Request: r}, nil
			})
			http.DefaultTransport = client.Transport
			info := &relaycommon.RelayInfo{OriginModelName: "gemini-3-pro-image", RelayMode: relayconstant.RelayModeImagesEdits, RequestURLPath: "/v1/images/edits", ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI, UpstreamModelName: "gemini-3-pro-image-preview", ChannelBaseUrl: "https://api.apib.ai"}}
			// First emulate the primary attempt returning a timeout without emitting
			// content. Fallback must retain the parsed client request and its references.
			primaryErr := service.RelayErrorHandler(c.Request.Context(), &http.Response{StatusCode: 524, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"error":{"message":"timeout"}}`))}, false)
			require.Equal(t, 524, primaryErr.StatusCode)
			mapped := *req
			mapped.Model = info.UpstreamModelName
			wire, err := (&Adaptor{}).ConvertImageRequest(c, info, mapped)
			require.NoError(t, err)
			raw, err := common.Marshal(wire)
			require.NoError(t, err)
			common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, "https://api.apib.ai")
			common.SetContextKey(c, constant.ContextKeyChannelKey, "test-provider-key")
			response, err := (&Adaptor{}).DoRequest(c, info, bytes.NewReader(raw))
			require.NoError(t, err)
			_, apiErr := OpenaiHandlerWithUsage(c, info, response.(*http.Response))
			require.Nil(t, apiErr)
			require.Equal(t, 1, submits)
			require.Greater(t, polls, 0)
			require.Equal(t, ct, c.GetHeader("Content-Type"))
			require.Equal(t, "gemini-3-pro-image", req.Model)
			require.Equal(t, "b64_json", req.ResponseFormat)
			var result struct {
				Data []struct {
					B64 string `json:"b64_json"`
				} `json:"data"`
			}
			require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &result))
			require.Len(t, result.Data, 1)
			require.Equal(t, base64.StdEncoding.EncodeToString(pngBody.Bytes()), result.Data[0].B64)
			require.NotContains(t, recorder.Body.String(), "test-provider-task")
		})
	}
}
