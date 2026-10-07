package controller

import (
	"bytes"
	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"mime/multipart"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSeedanceControlledPortraitStorage(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SEEDANCE_PORTRAIT_DIR", dir)
	t.Setenv("SEEDANCE_PORTRAIT_SIGNING_KEY", strings.Repeat("k", 32))
	t.Setenv("SEEDANCE_CALLBACK_ORIGIN", "https://apimaster.example")
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, e := writer.CreateFormFile("file", "test.png")
	require.NoError(t, e)
	_, e = part.Write([]byte{137, 80, 78, 71, 13, 10, 26, 10})
	require.NoError(t, e)
	require.NoError(t, writer.Close())
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Set("id", 1)
	c.Request = httptest.NewRequest("POST", "/", &body)
	c.Request.Header.Set("Content-Type", writer.FormDataContentType())
	UploadSeedancePortrait(c)
	require.Equal(t, 200, recorder.Code)
	var response struct {
		Data struct {
			ID  string `json:"upload_id"`
			URL string `json:"url"`
		}
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	require.NotContains(t, recorder.Body.String(), strings.Repeat("k", 32))
	info, e := os.Stat(filepath.Join(dir, response.Data.ID))
	require.NoError(t, e)
	require.EqualValues(t, 0600, info.Mode().Perm())
	parts := strings.Split(response.Data.URL, "/")
	expires, sig := parts[len(parts)-2], parts[len(parts)-1]
	download := func(signature string) int {
		w := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(w)
		ctx.Request = httptest.NewRequest("GET", "/", nil)
		ctx.Params = gin.Params{{Key: "upload_id", Value: response.Data.ID}, {Key: "expires", Value: expires}, {Key: "signature", Value: signature}}
		DownloadSeedancePortrait(ctx)
		require.Equal(t, "no-store, private", w.Header().Get("Cache-Control"))
		return w.Code
	}
	require.Equal(t, 404, download("forged"))
	require.Equal(t, 200, download(sig))
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)
	ctx.Set("id", 2)
	ctx.Params = gin.Params{{Key: "upload_id", Value: response.Data.ID}}
	DeleteSeedancePortraitUpload(ctx)
	require.Equal(t, 404, w.Code)
	require.NoError(t, os.Chtimes(filepath.Join(dir, response.Data.ID), time.Now().Add(-25*time.Hour), time.Now().Add(-25*time.Hour)))
	require.Equal(t, 404, download(sig))
	w = httptest.NewRecorder()
	ctx, _ = gin.CreateTestContext(w)
	ctx.Set("id", 1)
	ctx.Params = gin.Params{{Key: "upload_id", Value: response.Data.ID}}
	DeleteSeedancePortraitUpload(ctx)
	require.Equal(t, 200, w.Code)
	_, e = os.Stat(filepath.Join(dir, response.Data.ID))
	require.True(t, os.IsNotExist(e))
}
