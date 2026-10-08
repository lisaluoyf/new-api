package service

import (
	"bytes"
	"image"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestSeedanceMultipartStoresPublicReference(t *testing.T) {
	oldDir, oldBase := imageCacheDir, imageCachePublicBase
	imageCacheDir, imageCachePublicBase = t.TempDir(), "https://apimaster.ai/imgs/"
	t.Cleanup(func() { imageCacheDir, imageCachePublicBase = oldDir, oldBase })
	var pngBytes bytes.Buffer
	require.NoError(t, png.Encode(&pngBytes, image.NewRGBA(image.Rect(0, 0, 2, 2))))
	for _, model := range []string{"seedance-2.5", "seedance-2.0"} {
		for _, field := range []string{"input_reference", "images", "first_frame_image"} {
			t.Run(model+"/"+field, func(t *testing.T) {
				body := bytes.NewBuffer(nil)
				w := multipart.NewWriter(body)
				for key, value := range map[string]string{"model": model, "prompt": "scene", "seconds": "4", "size": "720x1280", "generate_audio": "false", "watermark": "false", "seed": "0"} {
					require.NoError(t, w.WriteField(key, value))
				}
				if field != "first_frame_image" {
					require.NoError(t, w.WriteField("image_urls", `["https://example.com/existing.png"]`))
				}
				f, err := w.CreateFormFile(field, "reference.png")
				require.NoError(t, err)
				_, err = f.Write(pngBytes.Bytes())
				require.NoError(t, err)
				require.NoError(t, w.Close())
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", body)
				c.Request.Header.Set("Content-Type", w.FormDataContentType())
				defer common.CleanupBodyStorage(c)
				require.NoError(t, NormalizeSeedanceMultipart(c))
				require.Equal(t, "application/json", c.GetHeader("Content-Type"))
				fields, err := SeedanceRequestFields(c)
				require.NoError(t, err)
				require.Equal(t, model, fields["model"])
				require.Equal(t, false, fields["generate_audio"])
				require.Equal(t, false, fields["watermark"])
				require.EqualValues(t, 0, fields["seed"])
				var url string
				if field == "first_frame_image" {
					url = fields[field].(string)
				} else {
					refs := fields["image_urls"].([]any)
					require.Len(t, refs, 2)
					require.Equal(t, "https://example.com/existing.png", refs[0])
					url = refs[1].(string)
				}
				require.True(t, strings.HasPrefix(url, imageCachePublicBase+"media_upload_"), url)
				stored, err := os.ReadFile(filepath.Join(imageCacheDir, strings.TrimPrefix(url, imageCachePublicBase)))
				require.NoError(t, err)
				require.Equal(t, pngBytes.Bytes(), stored)
				// Retries read the normalized JSON instead of uploading the file again.
				again, err := SeedanceRequestFields(c)
				require.NoError(t, err)
				require.Equal(t, fields, again)
			})
		}
	}
}

func TestSeedanceMultipartStorageFailureStopsRequest(t *testing.T) {
	oldDir := imageCacheDir
	imageCacheDir = filepath.Join(t.TempDir(), "blocker", "images")
	require.NoError(t, os.WriteFile(filepath.Dir(imageCacheDir), []byte("block directory creation"), 0600))
	t.Cleanup(func() { imageCacheDir = oldDir })
	var pngBytes bytes.Buffer
	require.NoError(t, png.Encode(&pngBytes, image.NewRGBA(image.Rect(0, 0, 2, 2))))
	body := bytes.NewBuffer(nil)
	w := multipart.NewWriter(body)
	require.NoError(t, w.WriteField("model", "seedance-2.5"))
	f, err := w.CreateFormFile("input_reference", "reference.png")
	require.NoError(t, err)
	_, err = f.Write(pngBytes.Bytes())
	require.NoError(t, err)
	require.NoError(t, w.Close())
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", body)
	c.Request.Header.Set("Content-Type", w.FormDataContentType())
	defer common.CleanupBodyStorage(c)
	require.ErrorContains(t, NormalizeSeedanceMultipart(c), "cannot store reference image")
}
