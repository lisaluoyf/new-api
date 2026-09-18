package helper

import (
	"bytes"
	"encoding/base64"
	"mime/multipart"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestConvertImageEditsToGenerationMultipart(t *testing.T) {
	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	for k, v := range map[string]string{"model": "gpt-image-2", "prompt": "edit", "resolution": "2k", "size": "1:1", "n": "2", "output_compression": "0", "watermark": "false", "response_format": "b64_json"} {
		require.NoError(t, w.WriteField(k, v))
	}
	for _, content := range []string{"first image", "second image"} {
		f, err := w.CreateFormFile("image[]", "ref.png")
		require.NoError(t, err)
		_, err = f.Write([]byte(content))
		require.NoError(t, err)
	}
	require.NoError(t, w.Close())
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/images/edits", bytes.NewReader(b.Bytes()))
	ct := w.FormDataContentType()
	c.Request.Header.Set("Content-Type", ct)
	t.Cleanup(func() { common.CleanupBodyStorage(c) })
	for i := 0; i < 2; i++ {
		r, err := ConvertImageEditsToGeneration(c, dto.ImageRequest{Model: "mapped-official"})
		require.NoError(t, err)
		require.Equal(t, "mapped-official", r.Model)
		require.Equal(t, "2k", r.Resolution)
		require.EqualValues(t, 2, *r.N)
		require.Equal(t, "0", string(r.OutputCompression))
		require.NotNil(t, r.Watermark)
		require.False(t, *r.Watermark)
		require.Empty(t, r.ResponseFormat)
		require.Len(t, r.ImageUrls, 2)
		require.Equal(t, "data:image/png;base64,"+base64.StdEncoding.EncodeToString([]byte("first image")), r.ImageUrls[0])
		require.Equal(t, "/v1/images/edits", c.Request.URL.Path)
		require.Equal(t, ct, c.GetHeader("Content-Type"))
	}
	form, err := common.ParseMultipartFormReusable(c)
	require.NoError(t, err)
	defer form.RemoveAll()
	require.Len(t, form.File["image[]"], 2)
}

func TestConvertImageEditsToGenerationJSON(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/images/edits", nil)
	c.Request.Header.Set("Content-Type", "application/json")
	for _, tc := range []struct {
		body    string
		count   int
		wantErr bool
	}{
		{`{"model":"gpt-image-2","image":"https://example.test/a.png"}`, 1, false},
		{`{"image_urls":["a"],"image":["b","c"],"images":["d"]}`, 4, false},
		{`{"prompt":"edit"}`, 0, true},
		{`{"image":""}`, 0, true},
		{`{"image":{"url":"x"}}`, 0, true},
		{`{"image":"a","mask":"mask"}`, 0, true},
	} {
		var original dto.ImageRequest
		require.NoError(t, common.Unmarshal([]byte(tc.body), &original))
		got, err := ConvertImageEditsToGeneration(c, original)
		if tc.wantErr {
			require.Error(t, err)
			continue
		}
		require.NoError(t, err)
		require.Len(t, got.ImageUrls, tc.count)
		require.Empty(t, got.Image)
		require.Empty(t, got.Images)
	}
}
