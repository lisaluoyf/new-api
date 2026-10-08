package openai

import (
	"bytes"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestSubrouterGeminiImageWirePreservesResolution(t *testing.T) {
	for _, model := range []string{"gemini-3.1-flash-image", "gemini-3-pro-image-preview"} {
		for _, tier := range []struct{ resolution, size string }{{"1k", "1024x1024"}, {"2K", "2048x2048"}, {"4k", "4096x4096"}} {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", "/v1/images/generations/async", nil)
			c.Request.Header.Set("Content-Type", "application/json")
			info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeImagesGenerations, ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://subrouter.ai"}}
			wire, err := (&Adaptor{}).ConvertImageRequest(c, info, dto.ImageRequest{Model: model, Prompt: "background only", Size: "1:1", Resolution: tier.resolution})
			require.NoError(t, err)
			raw, err := common.Marshal(wire)
			require.NoError(t, err)
			var sent dto.ImageRequest
			require.NoError(t, common.Unmarshal(raw, &sent))
			require.Equal(t, tier.size, sent.Size)
			require.Equal(t, tier.resolution, sent.Resolution)
			require.Equal(t, model, sent.Model)
		}
	}
}

func TestSubrouterGeminiReferencesUploadActualImageBytes(t *testing.T) {
	image, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+/l9sAAAAASUVORK5CYII=")
	require.NoError(t, err)
	oldDownload := subrouterGeminiDownload
	t.Cleanup(func() { subrouterGeminiDownload = oldDownload })
	ref := "https://example.com/reference.png?signature=a%2Fb&expires=123"
	subrouterGeminiDownload = func(reference string, reason ...string) (*http.Response, error) {
		require.Equal(t, ref, reference)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(image))}, nil
	}
	for _, model := range []string{"gemini-3-pro-image-preview", "gemini-3.1-flash-image"} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("POST", "/v1/images/generations/async", nil)
		c.Request.Header.Set("Content-Type", "application/json")
		info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeImagesGenerations, RequestURLPath: c.Request.URL.Path, ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://subrouter.ai"}}
		for attempt := 0; attempt < 2; attempt++ {
			wire, err := (&Adaptor{}).ConvertImageRequest(c, info, dto.ImageRequest{Model: model, Prompt: "background only", Size: "1:1", Resolution: "2k", ResponseFormat: "url", ImageUrls: []string{ref}})
			require.NoError(t, err)
			require.True(t, c.GetBool("subrouter_gemini_native_edit"))
			request := httptest.NewRequest("POST", "/v1/images/edits", wire.(*bytes.Buffer))
			request.Header.Set("Content-Type", c.GetString("subrouter_gemini_edit_content_type"))
			require.NoError(t, request.ParseMultipartForm(1<<20))
			t.Cleanup(func() { request.MultipartForm.RemoveAll() })
			require.Equal(t, "2048x2048", request.FormValue("size"))
			require.Equal(t, "2k", request.FormValue("resolution"))
			require.Equal(t, model, request.FormValue("model"))
			require.Equal(t, "url", request.FormValue("response_format"))
			file, _, err := request.FormFile("image")
			require.NoError(t, err)
			actual, err := io.ReadAll(file)
			file.Close()
			require.NoError(t, err)
			require.Equal(t, image, actual)
			require.Equal(t, "application/json", c.Request.Header.Get("Content-Type"))
			require.Equal(t, "/v1/images/generations/async", info.RequestURLPath)
		}
	}
	subrouterGeminiDownload = func(string, ...string) (*http.Response, error) {
		return &http.Response{StatusCode: 403, Body: io.NopCloser(bytes.NewReader([]byte("expired")))}, nil
	}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/images/generations/async", nil)
	c.Request.Header.Set("Content-Type", "application/json")
	_, err = (&Adaptor{}).ConvertImageRequest(c, &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://subrouter.ai"}}, dto.ImageRequest{Model: "gemini-3-pro-image-preview", ImageUrls: []string{ref}})
	require.Error(t, err)
	require.NotContains(t, err.Error(), "signature")
	require.False(t, c.GetBool("subrouter_gemini_native_edit"))
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
