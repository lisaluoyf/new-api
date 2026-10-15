package relay

import (
	"bytes"
	"encoding/base64"
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

func TestSubrouterGeminiMultipartFallbackRetainsReferenceWithPassThrough(t *testing.T) {
	image, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+/l9sAAAAASUVORK5CYII=")
	require.NoError(t, err)
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for key, value := range map[string]string{"model": "gemini-3-pro-image", "prompt": "edit", "resolution": "2K", "size": "1:1", "response_format": "b64_json"} {
		require.NoError(t, writer.WriteField(key, value))
	}
	part, err := writer.CreateFormFile("image", "reference.png")
	require.NoError(t, err)
	_, err = part.Write(image)
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	ct := writer.FormDataContentType()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/images/edits", bytes.NewReader(body.Bytes()))
	c.Request.Header.Set("Content-Type", ct)
	t.Cleanup(func() { common.CleanupBodyStorage(c) })
	request, err := helper.GetAndValidOpenAIImageRequest(c, relayconstant.RelayModeImagesEdits)
	require.NoError(t, err)
	info := &relaycommon.RelayInfo{OriginModelName: "gemini-3-pro-image", RelayMode: relayconstant.RelayModeImagesEdits, RequestURLPath: "/v1/images/edits", Request: request}
	common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeOpenAI)
	common.SetContextKey(c, constant.ContextKeyOriginalModel, "gemini-3-pro-image")
	common.SetContextKey(c, constant.ContextKeyChannelSetting, dto.ChannelSettings{PassThroughBodyEnabled: true})
	service.InitHttpClient()
	client := service.GetHttpClient()
	old := client.Transport
	t.Cleanup(func() { client.Transport = old })
	calls := 0
	client.Transport = geminiImageTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			require.Equal(t, "subrouter.ai", r.URL.Host)
			require.Equal(t, "/v1/images/edits", r.URL.Path)
			require.NoError(t, r.ParseMultipartForm(1<<20))
			defer r.MultipartForm.RemoveAll()
			require.Equal(t, "url", r.FormValue("response_format"))
			require.Equal(t, "2048x2048", r.FormValue("size"))
			file, _, err := r.FormFile("image")
			require.NoError(t, err)
			actual, err := io.ReadAll(file)
			file.Close()
			require.NoError(t, err)
			require.Equal(t, image, actual)
		} else {
			require.Equal(t, "api.apib.ai", r.URL.Host)
			require.Equal(t, "/v1/images/generations", r.URL.Path)
			var sent dto.ImageRequest
			require.NoError(t, common.DecodeJson(r.Body, &sent))
			require.Equal(t, []string{"data:image/png;base64," + base64.StdEncoding.EncodeToString(image)}, sent.ImageUrls)
			require.Equal(t, "url", sent.ResponseFormat)
		}
		status := 524
		if calls > 1 {
			status = 503
		}
		return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"error":{"message":"test upstream failure"}}`)), Request: r}, nil
	})
	common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, "https://subrouter.ai")
	first := ImageHelper(c, info)
	require.NotNil(t, first)
	require.Equal(t, 524, first.StatusCode)
	common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, "https://api.apib.ai")
	second := ImageHelper(c, info)
	require.NotNil(t, second)
	require.Equal(t, 503, second.StatusCode)
	require.Equal(t, 2, calls)
	require.Equal(t, ct, c.GetHeader("Content-Type"))
	require.Equal(t, "b64_json", request.ResponseFormat)
}
