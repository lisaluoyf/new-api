package relay

import (
	"bytes"
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
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type geminiImageTransport func(*http.Request) (*http.Response, error)

func (f geminiImageTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestGeminiImageFallbackAdaptsEvenWithPassThrough(t *testing.T) {
	service.InitHttpClient()
	client := service.GetHttpClient()
	previous := client.Transport
	t.Cleanup(func() { client.Transport = previous })
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for k, v := range map[string]string{"model": "gemini-3-pro-image", "prompt": "edit", "resolution": "2K", "response_format": "b64_json"} {
		require.NoError(t, writer.WriteField(k, v))
	}
	file, err := writer.CreateFormFile("image", "ref.png")
	require.NoError(t, err)
	_, err = file.Write([]byte("original-reference"))
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	ct := writer.FormDataContentType()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/images/edits", bytes.NewReader(body.Bytes()))
	c.Request.Header.Set("Content-Type", ct)
	t.Cleanup(func() { common.CleanupBodyStorage(c) })
	req, err := helper.GetAndValidOpenAIImageRequest(c, relayconstant.RelayModeImagesEdits)
	require.NoError(t, err)
	info := &relaycommon.RelayInfo{OriginModelName: "gemini-3-pro-image", RelayMode: relayconstant.RelayModeImagesEdits, RequestURLPath: "/v1/images/edits", Request: req}
	common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeOpenAI)
	common.SetContextKey(c, constant.ContextKeyOriginalModel, "gemini-3-pro-image")
	common.SetContextKey(c, constant.ContextKeyChannelSetting, dto.ChannelSettings{PassThroughBodyEnabled: true})
	calls := 0
	client.Transport = geminiImageTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			require.Equal(t, "/v1/images/edits", r.URL.Path)
			require.Contains(t, r.Header.Get("Content-Type"), "multipart/form-data")
			raw, err := io.ReadAll(r.Body)
			require.NoError(t, err)
			require.Contains(t, string(raw), "original-reference")
		} else {
			require.Equal(t, "/v1/images/generations", r.URL.Path)
			require.Equal(t, "application/json", r.Header.Get("Content-Type"))
			var wire dto.ImageRequest
			require.NoError(t, common.DecodeJson(r.Body, &wire))
			require.Equal(t, "url", wire.ResponseFormat)
			require.Equal(t, "gemini-3-pro-image-preview", wire.Model)
			require.Len(t, wire.ImageUrls, 1)
			require.Equal(t, "data:image/png;base64,b3JpZ2luYWwtcmVmZXJlbmNl", wire.ImageUrls[0])
		}
		status := 524
		if calls > 1 {
			status = 503
		}
		return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"error":{"message":"test upstream unavailable"}}`)), Request: r}, nil
	})
	common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, "https://primary.example")
	primary := ImageHelper(c, info)
	require.NotNil(t, primary)
	require.Equal(t, 524, primary.StatusCode)
	require.Equal(t, ct, c.GetHeader("Content-Type"))
	common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, "https://api.apib.ai")
	c.Set("model_mapping", `{"gemini-3-pro-image":"gemini-3-pro-image-preview"}`)
	fallback := ImageHelper(c, info)
	require.NotNil(t, fallback)
	require.Equal(t, 503, fallback.StatusCode)
	require.Equal(t, 2, calls)
	require.Equal(t, ct, c.GetHeader("Content-Type"))
	require.Equal(t, "gemini-3-pro-image", req.Model)
}
