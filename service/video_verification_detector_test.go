package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
)

func TestClassifyVerifiedVideoRequiresFreshCompleteMatch(t *testing.T) {
	stale := true
	result := videoVerificationResult{Claimed: "SD2.0", Verdict: "match", Pipeline: "SD2.0", Family: "ark"}
	result.Baseline.Expires = time.Now().UTC().Add(24 * time.Hour).Format("2006-01-02")
	result.Baseline.Stale = &stale
	status, reason, checks := classifyVerifiedVideo(result, "seedance-2.0", time.Now().UTC())
	require.Equal(t, "notcomplete", status)
	require.Equal(t, "baseline_unavailable_or_expired", reason)
	require.Nil(t, checks)

	fresh := false
	result.Baseline.Stale = &fresh
	result.Checks = []model.VideoFingerprintCheck{
		{ID: "claim", Status: "pass", Value: "SD2.0"},
		{ID: "dimensions", Status: "pass", Value: "1280x720"},
		{ID: "family", Status: "pass", Value: "ark"},
		{ID: "x264", Status: "pass", Value: "superfast · crf 20.0"},
		{ID: "frames", Status: "pass", Value: "97"},
	}
	status, reason, checks = classifyVerifiedVideo(result, "seedance-2.0", time.Now().UTC())
	require.Equal(t, "pass", status)
	require.Equal(t, "match", reason)
	require.Len(t, checks, 5)
}

func TestClassifyVerifiedVideoOnlyAlertsKnownMismatch(t *testing.T) {
	fresh := false
	result := videoVerificationResult{Claimed: "SD2.0", Verdict: "mismatch", Pipeline: "SD2.5", Family: "ark"}
	result.Baseline.Expires = time.Now().UTC().Add(24 * time.Hour).Format("2006-01-02")
	result.Baseline.Stale = &fresh
	status, reason, _ := classifyVerifiedVideo(result, "seedance-2.0", time.Now().UTC())
	require.Equal(t, "suspicious", status)
	require.Equal(t, "version_mismatch", reason)
}

func TestClassifyVerifiedVideoDoesNotAlertUnknownOrIncompleteEvidence(t *testing.T) {
	for _, reason := range []string{"unknown_container", "tencent_pipeline", "no_x264_sei", "unknown_params", "probe_failed", "unsupported_encoding_fingerprint"} {
		t.Run(reason, func(t *testing.T) {
			fresh := false
			result := videoVerificationResult{Claimed: "SD2.0", Verdict: "unknown", Reason: reason}
			result.Baseline.Expires = "2026-11-01"
			result.Baseline.Stale = &fresh
			status, _, checks := classifyVerifiedVideo(result, "seedance-2.0", time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC))
			require.Equal(t, "notcomplete", status)
			require.Nil(t, checks)
		})
	}
	for _, checkStatus := range []string{"warn", "skip", "fail"} {
		fresh := false
		result := videoVerificationResult{Claimed: "SD2.0", Verdict: "match", Pipeline: "SD2.0", Family: "ark", Checks: []model.VideoFingerprintCheck{{ID: "frames", Status: checkStatus, Value: "100"}}}
		result.Baseline.Expires = "2026-11-01"
		result.Baseline.Stale = &fresh
		status, _, checks := classifyVerifiedVideo(result, "seedance-2.0", time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC))
		require.Equal(t, "notcomplete", status)
		require.Nil(t, checks)
	}
}

func TestVideoVerificationMediaURLUsesStoredKeyOnlyForProvider(t *testing.T) {
	base := "https://provider.example"
	channel := &model.Channel{BaseURL: &base, Key: "current-key-must-not-be-used"}
	task := &model.Task{TaskID: "public-id", PrivateData: model.TaskPrivateData{Key: "stored-key", UpstreamTaskID: "upstream-id"}}
	mediaURL, key := videoVerificationMediaURL(task, channel)
	require.Equal(t, "https://provider.example/v1/videos/upstream-id/content", mediaURL)
	require.Equal(t, "stored-key", key)
	task.PrivateData.UpstreamVideoURL = "https://cdn.example/video.mp4?signature=private"
	mediaURL, key = videoVerificationMediaURL(task, channel)
	require.Equal(t, task.PrivateData.UpstreamVideoURL, mediaURL)
	require.Empty(t, key)
}

func TestVideoVerificationDownloadRejectsPrivateTargetsAndRedirects(t *testing.T) {
	for _, target := range []string{"http://public.example/video.mp4", "https://127.0.0.1/video.mp4", "https://10.0.0.1/video.mp4", "https://169.254.169.254/video.mp4", "https://[::1]/video.mp4", "https://user:secret@example.com/video.mp4"} {
		file, err := downloadVerificationVideo(context.Background(), target, "secret")
		require.Error(t, err)
		require.Nil(t, file)
		require.NotContains(t, err.Error(), "secret")
	}
	client := verificationDownloadClient()
	original, err := http.NewRequest(http.MethodGet, "https://provider.example/video.mp4", nil)
	require.NoError(t, err)
	redirect, err := http.NewRequest(http.MethodGet, "https://cdn.example/video.mp4", nil)
	require.NoError(t, err)
	redirect.Header.Set("Authorization", "Bearer secret")
	require.NoError(t, client.CheckRedirect(redirect, []*http.Request{original}))
	require.Empty(t, redirect.Header.Get("Authorization"))
	redirect.URL.Host = "127.0.0.1"
	require.Error(t, client.CheckRedirect(redirect, []*http.Request{original}))
}

func TestVideoVerificationDetectorMultipartAndSingleResult(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "video-*.mp4")
	require.NoError(t, err)
	defer file.Close()
	_, err = file.WriteString("mock-video-content")
	require.NoError(t, err)
	_, err = file.Seek(0, 0)
	require.NoError(t, err)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		require.Equal(t, "/api/video/seedance-verify", request.URL.Path)
		require.NoError(t, request.ParseMultipartForm(1024))
		defer request.MultipartForm.RemoveAll()
		require.Equal(t, "sd25", request.FormValue("claim"))
		uploads := request.MultipartForm.File["files"]
		require.Len(t, uploads, 1)
		require.Equal(t, "verification.mp4", uploads[0].Filename)
		_, _ = writer.Write([]byte(`{"results":[{"claimed":"SD2.5","verdict":"unknown","reason":"unknown_container"}]}`))
	}))
	defer server.Close()
	t.Setenv("APIMASTER_FLASK_URL", server.URL)
	result, err := requestVideoVerification(context.Background(), file, "seedance-2.5")
	require.NoError(t, err)
	require.Equal(t, "SD2.5", result.Claimed)
	require.Equal(t, "unknown_container", result.Reason)
	require.False(t, strings.Contains(result.Reason, file.Name()))
}
