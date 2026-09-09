package helper

import (
	"bytes"
	"encoding/base64"
	"io"
	"mime/multipart"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func image25Context(t *testing.T, path, contentType string, body []byte) *gin.Context {
	t.Helper()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", path, bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", contentType)
	storage, err := common.CreateBodyStorage(body)
	require.NoError(t, err)
	c.Set(common.KeyBodyStorage, storage)
	t.Cleanup(func() { common.CleanupBodyStorage(c) })
	return c
}

func TestImage25MultipartReferenceRouting(t *testing.T) {
	for _, name := range []string{"gpt-image-2.5-sunburst", "gpt-image-2.5-flare"} {
		for _, path := range []string{"/v1/images/edits", "/v1/images/generations", "/v1/images/generations/async"} {
			t.Run(name+path, func(t *testing.T) {
				var body bytes.Buffer
				writer := multipart.NewWriter(&body)
				for k, v := range map[string]string{"model": name, "prompt": "change only the background", "resolution": "4K", "size": "1:1", "n": "2", "response_format": "b64_json", "output_compression": "0", "watermark": "false"} {
					require.NoError(t, writer.WriteField(k, v))
				}
				for _, contents := range []string{"reference-A", "reference-B"} {
					part, err := writer.CreateFormFile("image[]", "ref.png")
					require.NoError(t, err)
					_, err = io.WriteString(part, contents)
					require.NoError(t, err)
				}
				require.NoError(t, writer.Close())
				c := image25Context(t, path, writer.FormDataContentType(), body.Bytes())
				require.NoError(t, NormalizeGptImage25ReferenceRequest(c, name))
				require.Equal(t, "application/json", c.ContentType())
				wantPath := path
				if path == "/v1/images/edits" {
					wantPath = "/v1/images/generations"
				}
				require.Equal(t, wantPath, c.Request.URL.Path)
				require.Equal(t, name, service.PrepareGptImage2ModelRequest(c, name))
				filter := service.GptImage2ChannelPickFilter(c, name)
				require.NotNil(t, filter)
				require.True(t, filter(&model.Channel{Id: 59}))
				req, err := GetAndValidOpenAIImageRequest(c, relayconstant.RelayModeImagesGenerations)
				require.NoError(t, err)
				require.Equal(t, name, req.Model)
				require.Equal(t, "4K", req.EffectiveResolutionTier())
				require.EqualValues(t, 2, *req.N)
				require.Equal(t, []string{"data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte("reference-A")), "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte("reference-B"))}, req.ImageUrls)
				require.Equal(t, "0", string(req.OutputCompression))
				require.NotNil(t, req.Watermark)
				require.False(t, *req.Watermark)
				require.Empty(t, req.ResponseFormat)
				// Retrying reads the normalized body again, without losing references.
				require.NoError(t, NormalizeGptImage25ReferenceRequest(c, name))
				retry, err := GetAndValidOpenAIImageRequest(c, relayconstant.RelayModeImagesGenerations)
				require.NoError(t, err)
				require.Equal(t, req.ImageUrls, retry.ImageUrls)
			})
		}
	}
}

func TestImage25JSONEdit(t *testing.T) {
	for _, input := range []string{`"image":"https://example.com/ref.png"`, `"image_urls":["https://example.com/ref.png"]`} {
		c := image25Context(t, "/v1/images/edits", "application/json", []byte(`{"model":"gpt-image-2.5-flare","prompt":"edit","resolution":"2K",`+input+`}`))
		require.NoError(t, NormalizeGptImage25ReferenceRequest(c, "gpt-image-2.5-flare"))
		req, err := GetAndValidOpenAIImageRequest(c, relayconstant.RelayModeImagesGenerations)
		require.NoError(t, err)
		require.Equal(t, []string{"https://example.com/ref.png"}, req.ImageUrls)
		require.Empty(t, req.Image)
		require.Equal(t, "2K", req.EffectiveResolutionTier())
	}
}

func TestImage25RejectsMissingReferenceAndMask(t *testing.T) {
	for _, body := range []string{`{}`, `null`, `{"image_urls":"bad"}`, `{"image":{}}`, `{"image":"https://example.com/ref.png","mask":"mask.png"}`} {
		c := image25Context(t, "/v1/images/edits", "application/json", []byte(body))
		require.Error(t, NormalizeGptImage25ReferenceRequest(c, "gpt-image-2.5-flare"))
		require.Equal(t, "/v1/images/edits", c.Request.URL.Path)
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("mask", "mask.png")
	require.NoError(t, err)
	_, err = io.WriteString(part, "mask")
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	c := image25Context(t, "/v1/images/edits", writer.FormDataContentType(), body.Bytes())
	require.ErrorContains(t, NormalizeGptImage25ReferenceRequest(c, "gpt-image-2.5-sunburst"), "unsupported")
}

func TestImage25LeavesOtherModelsUnchanged(t *testing.T) {
	for _, name := range []string{"gpt-image-2", "gpt-image-2-official", "gpt-image-1", "gpt-image-2.5-unknown"} {
		c := image25Context(t, "/v1/images/edits", "application/json", []byte(`{}`))
		require.NoError(t, NormalizeGptImage25ReferenceRequest(c, name))
		require.Equal(t, "/v1/images/edits", c.Request.URL.Path)
		require.Equal(t, name == "gpt-image-2" || name == "gpt-image-2-official", service.IsGptImage2Family(name))
	}
}
