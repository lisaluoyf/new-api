package controller

import (
	"bytes"
	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSeedanceVerificationAPIUsesPageDetector(t *testing.T) {
	calls := 0
	detector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/video/seedance-verify", r.URL.Path)
		require.NoError(t, r.ParseMultipartForm(1024))
		defer r.MultipartForm.RemoveAll()
		require.Equal(t, "sd25", r.FormValue("claim"))
		file, _, err := r.FormFile("files")
		require.NoError(t, err)
		defer file.Close()
		raw, err := io.ReadAll(file)
		require.NoError(t, err)
		require.Equal(t, "fake-video", string(raw))
		calls++
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"results":[{"claimed":"SD2.5","verdict":"match","pipeline":"SD2.5","family":"ark","baseline":{"expires":"2099-12-31","stale":false},"checks":[{"id":"claim","status":"pass","value":"SD2.5"},{"id":"dimensions","status":"pass","value":"854x480"},{"id":"family","status":"pass","value":"ark"},{"id":"x264","status":"pass","value":"superfast"},{"id":"frames","status":"pass","value":"121"}]}]}`)
	}))
	defer detector.Close()
	t.Setenv("APIMASTER_FLASK_URL", detector.URL)
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	require.NoError(t, writer.WriteField("model", "seedance-2.5"))
	file, err := writer.CreateFormFile("file", "video.mp4")
	require.NoError(t, err)
	_, err = file.Write([]byte("fake-video"))
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest("POST", "/v1/videos/seedance-verify", &body)
	c.Request.Header.Set("Content-Type", writer.FormDataContentType())
	VerifySeedanceVideo(c)
	require.Equal(t, 200, recorder.Code)
	var payload map[string]any
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &payload))
	require.Equal(t, "pass", payload["status"])
	require.Equal(t, "seedance-2.5", payload["model"])
	require.NotZero(t, payload["detected_at"])
	require.Equal(t, 1, calls)
}

func TestSeedanceVerificationAPIRejectsInvalidModelsAndPrivateURLs(t *testing.T) {
	for _, body := range []string{`{"model":"gpt-4","video_url":"https://example.com/a.mp4"}`, `{"model":"seedance-2.5","video_url":"https://127.0.0.1/a.mp4"}`, `{"model":"seedance-2.5"}`} {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest("POST", "/v1/videos/seedance-verify", strings.NewReader(body))
		c.Request.Header.Set("Content-Type", "application/json")
		VerifySeedanceVideo(c)
		require.GreaterOrEqual(t, recorder.Code, 400)
	}
}
