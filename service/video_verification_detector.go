package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

const videoVerificationMaxBytes int64 = 500 * 1024 * 1024

type videoVerificationProvenance struct {
	FingerprintModelVersion string
	DetectorVersion         string
	BaselineSHA256          string
}

type videoVerificationResult struct {
	FingerprintModelVersion string `json:"fingerprint_model_version"`
	DetectorVersion         string `json:"detector_version"`
	Claimed                 string `json:"claimed"`
	Verdict                 string `json:"verdict"`
	Reason                  string `json:"reason"`
	Pipeline                string `json:"pipeline"`
	Family                  string `json:"family"`
	Baseline                struct {
		Expires        string            `json:"expires"`
		ClaimedModel   string            `json:"claimed_model"`
		ArtifactSHA256 map[string]string `json:"artifact_sha256"`
		Stale          *bool             `json:"stale"`
	} `json:"baseline"`
	Checks []model.VideoFingerprintCheck `json:"checks"`
}

func classifyVerifiedVideo(result videoVerificationResult, normalized string, now time.Time) (string, string, []model.VideoFingerprintCheck) {
	expected := "SD2.0"
	if normalized == "seedance-2.5" {
		expected = "SD2.5"
	}
	expires, err := time.Parse("2006-01-02", result.Baseline.Expires)
	if err != nil || result.Baseline.Stale == nil || *result.Baseline.Stale || !now.Before(expires.Add(24*time.Hour)) {
		return "notcomplete", "baseline_unavailable_or_expired", nil
	}
	knownPipeline := result.Pipeline == "SD2.0" || result.Pipeline == "SD2.5"
	if normalized != "seedance-2.5" && normalized != "seedance-2.0" && normalized != "seedance-2.0-fast" && normalized != "seedance-2.0-mini" {
		if result.Baseline.ClaimedModel != normalized || result.FingerprintModelVersion == "" || !strings.HasPrefix(result.Claimed, "SD") {
			return "notcomplete", "unexpected_claim", nil
		}
		expected = result.Claimed
		knownPipeline = strings.HasPrefix(result.Pipeline, "SD")
	}
	if result.Claimed != expected {
		return "notcomplete", "unexpected_claim", nil
	}
	if result.Family != "ark" || !knownPipeline {
		return "notcomplete", "unknown_fingerprint", nil
	}
	if result.Verdict == "mismatch" && result.Pipeline != expected {
		return "suspicious", "version_mismatch", result.Checks
	}
	if result.Verdict != "match" || result.Pipeline != expected {
		return "notcomplete", "inconclusive_verdict", nil
	}
	for _, check := range result.Checks {
		if check.Status == "fail" && (check.ID == "claim" || check.ID == "family" || check.ID == "x264") {
			return "suspicious", "known_fingerprint_failed", result.Checks
		}
	}
	encoded, err := common.Marshal(result.Checks)
	if err != nil {
		return "notcomplete", "invalid_evidence", nil
	}
	checks := model.PublicVideoFingerprintChecks(string(encoded))
	if checks == nil {
		return "notcomplete", "incomplete_evidence", result.Checks
	}
	return "pass", "match", checks
}

func verifyDeliveredVideoWithProvenance(ctx context.Context, task *model.Task, normalized string) (string, string, []model.VideoFingerprintCheck, videoVerificationProvenance) {
	channel, err := model.CacheGetChannel(task.ChannelId)
	if err != nil {
		return "notcomplete", "channel_unavailable", nil, videoVerificationProvenance{}
	}
	if strings.TrimSpace(channel.GetSetting().Proxy) != "" {
		return "notcomplete", "proxy_download_not_supported", nil, videoVerificationProvenance{}
	}
	mediaURL, key := videoVerificationMediaURL(task, channel)
	file, err := downloadVerificationVideo(ctx, mediaURL, key)
	if err != nil {
		return "notcomplete", "video_download_failed", nil, videoVerificationProvenance{}
	}
	defer func() { file.Close(); os.Remove(file.Name()) }()
	result, err := requestVideoVerification(ctx, file, normalized)
	if err != nil {
		return "notcomplete", "detector_unavailable", nil, videoVerificationProvenance{}
	}
	status, reason, checks := classifyVerifiedVideo(result, normalized, time.Now().UTC())
	raw, _ := common.Marshal(result.Baseline.ArtifactSHA256)
	return status, reason, checks, videoVerificationProvenance{FingerprintModelVersion: result.FingerprintModelVersion, DetectorVersion: result.DetectorVersion, BaselineSHA256: string(raw)}
}

func videoVerificationMediaURL(task *model.Task, channel *model.Channel) (string, string) {
	mediaURL := strings.TrimSpace(task.GetUpstreamVideoURL())
	if mediaURL == "" && IsDirectVideoMediaURL(task.GetResultURL()) {
		mediaURL = strings.TrimSpace(task.GetResultURL())
	}
	baseURL := strings.TrimRight(channel.GetBaseURL(), "/")
	if mediaURL == "" && baseURL != "" {
		mediaURL = fmt.Sprintf("%s/v1/videos/%s/content", baseURL, url.PathEscape(task.GetUpstreamTaskID()))
		return mediaURL, task.PrivateData.Key
	}
	media, mediaErr := url.Parse(mediaURL)
	base, baseErr := url.Parse(baseURL)
	if mediaErr == nil && baseErr == nil && strings.EqualFold(media.Host, base.Host) && media.Scheme == base.Scheme {
		return mediaURL, task.PrivateData.Key
	}
	return mediaURL, ""
}

func verificationDownloadClient() *http.Client {
	client := TaskWebhookHTTPClient()
	client.Timeout = videoVerificationTimeout
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many media redirects")
		}
		if _, err := ValidateTaskWebhookURL(req.URL.String()); err != nil {
			return err
		}
		if !strings.EqualFold(req.URL.Host, via[0].URL.Host) {
			req.Header.Del("Authorization")
		}
		return nil
	}
	return client
}

func downloadVerificationVideo(ctx context.Context, mediaURL, key string) (*os.File, error) {
	if _, err := ValidateTaskWebhookURL(mediaURL); err != nil {
		return nil, errors.New("media URL rejected")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, mediaURL, nil)
	if err != nil {
		return nil, errors.New("invalid media request")
	}
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	client := verificationDownloadClient()
	defer client.CloseIdleConnections()
	response, err := client.Do(req)
	if err != nil {
		return nil, errors.New("media request failed")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || response.ContentLength > videoVerificationMaxBytes {
		return nil, errors.New("media response rejected")
	}
	file, err := os.CreateTemp("", "seedance-verification-*.mp4")
	if err != nil {
		return nil, err
	}
	count, err := io.Copy(file, io.LimitReader(response.Body, videoVerificationMaxBytes+1))
	if err != nil || count == 0 || count > videoVerificationMaxBytes {
		file.Close()
		os.Remove(file.Name())
		return nil, errors.New("media download incomplete or too large")
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		file.Close()
		os.Remove(file.Name())
		return nil, err
	}
	return file, nil
}

func requestVideoVerification(ctx context.Context, file *os.File, normalized string) (videoVerificationResult, error) {
	var result videoVerificationResult
	flaskURL := strings.TrimRight(strings.TrimSpace(os.Getenv("APIMASTER_FLASK_URL")), "/")
	if flaskURL == "" {
		flaskURL = "http://127.0.0.1:7860"
	}
	reader, writer := io.Pipe()
	multipartWriter := multipart.NewWriter(writer)
	contentType := multipartWriter.FormDataContentType()
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		claim := "sd20"
		if normalized == "seedance-2.5" {
			claim = "sd25"
		}
		err := multipartWriter.WriteField("claim", claim)
		if err == nil {
			err = multipartWriter.WriteField("model", normalized)
		}
		if err == nil {
			var part io.Writer
			part, err = multipartWriter.CreateFormFile("files", "verification.mp4")
			if err == nil {
				_, err = io.Copy(part, file)
			}
		}
		if err == nil {
			err = multipartWriter.Close()
		}
		writer.CloseWithError(err)
	}()
	defer func() { reader.Close(); <-finished }()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, flaskURL+"/api/video/seedance-verify", reader)
	if err != nil {
		return result, err
	}
	req.Header.Set("Content-Type", contentType)
	client := &http.Client{Timeout: videoVerificationTimeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(req)
	if err != nil {
		return result, errors.New("detector request failed")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return result, errors.New("detector response rejected")
	}
	var payload struct {
		Results []videoVerificationResult `json:"results"`
	}
	if err := common.DecodeJson(io.LimitReader(response.Body, 1024*1024), &payload); err != nil || len(payload.Results) != 1 {
		return result, errors.New("invalid detector result")
	}
	return payload.Results[0], nil
}

type SeedanceVerificationResponse struct {
	Model      string                  `json:"model"`
	Status     string                  `json:"status"`
	Reason     string                  `json:"reason"`
	DetectedAt int64                   `json:"detected_at"`
	Result     videoVerificationResult `json:"result"`
}

func VerifySeedanceVideoFile(ctx context.Context, file *os.File, name string) (*SeedanceVerificationResponse, error) {
	name = model.NormalizeVerifiedVideoModel(name)
	if name == "" {
		return nil, fmt.Errorf("Unsupported Seedance model")
	}
	result, err := requestVideoVerification(ctx, file, name)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	status, reason, _ := classifyVerifiedVideo(result, name, now)
	return &SeedanceVerificationResponse{Model: name, Status: status, Reason: reason, DetectedAt: now.Unix(), Result: result}, nil
}

func VerifySeedanceVideoURL(ctx context.Context, mediaURL, name string) (*SeedanceVerificationResponse, error) {
	file, err := downloadVerificationVideo(ctx, mediaURL, "")
	if err != nil {
		return nil, err
	}
	defer func() { file.Close(); os.Remove(file.Name()) }()
	return VerifySeedanceVideoFile(ctx, file, name)
}
