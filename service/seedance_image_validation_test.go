package service

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestSeedanceMaterialImageValidation(t *testing.T) {
	for _, tc := range []struct {
		width, height int
		message       string
	}{
		{300, 300, ""}, {299, 300, "300-6000"}, {300, 1000, "0.4-2.5"}, {6001, 300, "300-6000"},
	} {
		var raw bytes.Buffer
		require.NoError(t, png.Encode(&raw, image.NewNRGBA(image.Rect(0, 0, tc.width, tc.height))))
		u := "data:image/png;base64," + base64.StdEncoding.EncodeToString(raw.Bytes())
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("POST", "/v1/videos/generations", nil)
		err := ValidateSeedanceImageInputs(c, map[string]any{"image_with_roles": []any{map[string]any{"url": u, "role": "reference_image"}}})
		if tc.message == "" {
			require.NoError(t, err)
		} else {
			require.ErrorContains(t, err, tc.message)
		}
		CleanupFileSources(c)
	}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/videos/generations", nil)
	require.NoError(t, ValidateSeedanceImageInputs(c, map[string]any{"image_urls": []any{"asset://owned-approved"}}))
	err := ValidateSeedanceImageInputs(c, map[string]any{"image_urls": []any{"data:image/png;base64,broken"}})
	require.ErrorContains(t, err, "cannot be verified")
	require.NotContains(t, err.Error(), "base64")
	fields := map[string]any{"image_urls": []any{"one", "one"}, "content": []any{map[string]any{"type": "image_url", "image_url": map[string]any{"url": "two"}}}, "metadata": map[string]any{"image_urls": []any{"three"}}}
	require.Equal(t, []string{"one", "two", "three"}, seedanceRawImageInputs(fields))
}

type seedanceImageValidationTransport func(*http.Request) (*http.Response, error)

func (f seedanceImageValidationTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestSeedanceImagePreflightBoundsFetchAndCachesDimensions(t *testing.T) {
	oldClient := httpClient
	settings := system_setting.GetFetchSetting()
	oldSettings := *settings
	oldWorker := system_setting.WorkerUrl
	settings.EnableSSRFProtection = false
	system_setting.WorkerUrl = ""
	t.Cleanup(func() { httpClient = oldClient; *settings = oldSettings; system_setting.WorkerUrl = oldWorker })
	var imageBody bytes.Buffer
	require.NoError(t, png.Encode(&imageBody, image.NewNRGBA(image.Rect(0, 0, 320, 400))))
	calls := 0
	httpClient = &http.Client{Transport: seedanceImageValidationTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		require.Equal(t, "GET", r.Method)
		deadline, ok := r.Context().Deadline()
		require.True(t, ok)
		require.LessOrEqual(t, time.Until(deadline), 20*time.Second)
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(imageBody.Bytes()))}, nil
	})}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/videos/generations", nil)
	fields := map[string]any{"image_urls": []any{"https://image.invalid/file.png?signature=private", "https://image.invalid/file.png?signature=private"}}
	require.NoError(t, ValidateSeedanceImageInputs(c, fields))
	require.NoError(t, ValidateSeedanceImageInputs(c, fields))
	require.Equal(t, 1, calls)
}
