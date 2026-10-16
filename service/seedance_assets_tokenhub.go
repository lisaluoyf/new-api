package service

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
)

// TokenHub uses distinct video and material credentials. Files live in the
// existing private /data volume, outside the Git checkout, and are channel scoped.
type tokenHubAssetCredentials struct {
	AccessKeyID     string `json:"access_key_id"`
	SecretAccessKey string `json:"secret_access_key"`
}

func IsTokenHubSeedanceBaseURL(base string) bool {
	u, err := url.Parse(base)
	return err == nil && u.Scheme == "https" && u.Host == "ai-tokenhub.ai" && u.User == nil
}
func isTokenHubSeedanceChannel(ch *model.Channel) bool {
	return ch != nil && ch.Type == constant.ChannelTypeDoubaoVideo && IsTokenHubSeedanceBaseURL(ch.GetBaseURL())
}
func SupportsRetainedSeedanceAutomaticReview(channelID int) bool {
	if channelID == 292 {
		return true
	}
	if channelID != 297 && channelID != 298 {
		return false
	}
	ch, err := model.GetChannelById(channelID, true)
	return err == nil && (isTokenHubSeedanceChannel(ch) || isCtaiSeedanceChannel(ch))
}
func loadTokenHubAssetCredentials(channelID int) (tokenHubAssetCredentials, error) {
	var c tokenHubAssetCredentials
	dir := os.Getenv("TOKENHUB_ASSET_CREDENTIALS_DIR")
	if dir == "" {
		dir = "/data/tokenhub-assets"
	}
	f, err := os.Open(filepath.Join(dir, "channel-"+strconv.Itoa(channelID)+".json"))
	if err != nil {
		return c, seedanceError(503, "TokenHub material credentials are not configured", "credentials_not_configured")
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() || st.Mode().Perm()&0077 != 0 || st.Size() > 8192 {
		return c, seedanceError(503, "TokenHub material credentials must be a private regular file", "credentials_not_configured")
	}
	raw, err := io.ReadAll(io.LimitReader(f, 8193))
	if err != nil || common.Unmarshal(raw, &c) != nil || !strings.HasPrefix(c.AccessKeyID, "pak_") || !strings.HasPrefix(c.SecretAccessKey, "psk_") || strings.ContainsAny(c.AccessKeyID+c.SecretAccessKey, " \r\n\t:") {
		return c, seedanceError(503, "TokenHub material credentials are invalid", "credentials_not_configured")
	}
	return c, nil
}
func tokenHubCredentialFingerprint(c tokenHubAssetCredentials) string {
	return SeedanceKeyFingerprint(c.AccessKeyID + ":" + c.SecretAccessKey)
}
func bindTokenHubAssetContext(r *model.SeedanceResource) error {
	c, err := loadTokenHubAssetCredentials(r.ChannelID)
	if err != nil {
		return err
	}
	r.ProjectName = "default"
	r.CredentialFingerprint = tokenHubCredentialFingerprint(c)
	return nil
}
func tokenHubAssetRequest(ctx context.Context, r *model.SeedanceResource, ch *model.Channel, action string, payload map[string]any, exposeReviewError bool) (map[string]any, error) {
	if !isTokenHubSeedanceChannel(ch) {
		return nil, seedanceError(503, "Invalid TokenHub media gateway")
	}
	c, err := loadTokenHubAssetCredentials(r.ChannelID)
	if err != nil {
		return nil, err
	}
	if r.CredentialFingerprint == "" || r.CredentialFingerprint != tokenHubCredentialFingerprint(c) {
		return nil, seedanceError(409, "Media library account changed; the original account is required", "asset_context_incompatible")
	}
	// This adapter is deliberately limited to virtual material operations. Owner
	// H5 verification has a different contract and is not implied by auto review.
	switch action {
	case "CreateAssetGroup", "GetAssetGroup", "UpdateAssetGroup", "DeleteAssetGroup", "CreateAsset", "GetAsset", "UpdateAsset", "DeleteAsset", "ListAssets", "ListAssetGroups", "GetModerationResult":
	default:
		return nil, seedanceError(400, "Unsupported TokenHub material operation")
	}
	fields := make(map[string]any, len(payload)+1)
	for k, v := range payload {
		fields[k] = v
	}
	fields["ProjectName"] = "default"
	raw, err := common.Marshal(fields)
	if err != nil {
		return nil, err
	}
	endpoint := "https://ai-tokenhub.ai/ark?" + url.Values{"Action": {action}, "Version": {"2024-01-01"}}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.AccessKeyID+":"+c.SecretAccessKey)
	client := &http.Client{Timeout: 45 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return nil, seedanceError(502, "Material request could not be confirmed; query existing resources before retrying", "submission_unknown")
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, (4<<20)+1))
	var envelope map[string]any
	if err != nil || len(body) > 4<<20 || common.Unmarshal(body, &envelope) != nil || envelope == nil {
		return nil, seedanceError(502, "Invalid material response", "submission_unknown")
	}
	detail, failed := envelope["ResponseError"].(map[string]any)
	if !failed {
		meta, _ := envelope["ResponseMetadata"].(map[string]any)
		detail, failed = meta["Error"].(map[string]any)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || failed || envelope["error"] != nil {
		status := resp.StatusCode
		code := "media_library_error"
		message := "TokenHub material request failed"
		if status < 400 || status >= 500 {
			status = 502
			code = "submission_unknown"
		}
		if exposeReviewError && resp.StatusCode >= 400 && resp.StatusCode < 500 {
			if detail == nil {
				detail, _ = envelope["error"].(map[string]any)
			}
			reason := seedanceString(detail, "Message", "message")
			if reason == "" {
				reason = seedanceString(envelope, "message")
			}
			if reason != "" {
				sensitive := append([]string{c.AccessKeyID, c.SecretAccessKey, r.SourceURL, r.UpstreamID}, ch.GetKeys()...)
				message = sanitizeTaskFailure(reason, sensitive...)
			}
			code = "invalid_asset_material"
		}
		return nil, seedanceError(status, message, code)
	}
	if _, ok := envelope["Result"].(map[string]any); !ok {
		return nil, seedanceError(502, "Material response has no result", "submission_unknown")
	}
	return envelope, nil
}

// TokenHub returns video error codes as a top-level string, while Ark uses an
// error object. Normalize only that documented shape for shared relay handling.
func NormalizeTokenHubVideoResponse(resp *http.Response) (*http.Response, error) {
	if resp == nil || resp.Body == nil {
		return resp, nil
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, (4<<20)+1))
	_ = resp.Body.Close()
	if err != nil {
		return nil, err
	}
	if len(raw) > 4<<20 {
		return nil, seedanceError(502, "Video response is too large")
	}
	var fields map[string]json.RawMessage
	if common.Unmarshal(raw, &fields) == nil {
		// The deployed v2 query endpoint wraps Ark content/usage in resultSummary.
		// Keep the platform task id; copy only generation-result fields.
		var summary map[string]json.RawMessage
		if common.Unmarshal(fields["resultSummary"], &summary) == nil && summary != nil {
			for _, key := range []string{"content", "usage", "duration", "resolution", "ratio", "seed", "framespersecond", "tools", "service_tier", "draft", "error"} {
				if value, ok := summary[key]; ok {
					fields[key] = value
				}
			}
		}
		var wrapperErrorCode, wrapperErrorMessage string
		_ = common.Unmarshal(fields["errorCode"], &wrapperErrorCode)
		_ = common.Unmarshal(fields["errorMessage"], &wrapperErrorMessage)
		if wrapperErrorCode != "" || wrapperErrorMessage != "" {
			fields["error"], _ = common.Marshal(map[string]string{"code": wrapperErrorCode, "message": wrapperErrorMessage})
		}
		if _, ok := fields["content"]; !ok {
			var videoURL string
			if common.Unmarshal(fields["videoUrl"], &videoURL) == nil && videoURL != "" {
				fields["content"], _ = common.Marshal(map[string]string{"video_url": videoURL})
			}
		}
		var status string
		_ = common.Unmarshal(fields["status"], &status)
		if status == "expired" || status == "cancelled" || status == "canceled" {
			fields["status"], _ = common.Marshal("failed")
			if wrapperErrorCode == "" && wrapperErrorMessage == "" {
				fields["error"], _ = common.Marshal(map[string]string{"code": status, "message": "Video task " + status})
			}
		}
		var code string
		if common.Unmarshal(fields["error"], &code) == nil && code != "" {
			var message string
			_ = common.Unmarshal(fields["message"], &message)
			fields["error"], err = common.Marshal(map[string]string{"code": code, "message": message})
			if err != nil {
				return nil, err
			}
		}
		raw, err = common.Marshal(fields)
		if err != nil {
			return nil, err
		}
	}
	resp.Body = io.NopCloser(bytes.NewReader(raw))
	resp.ContentLength = int64(len(raw))
	resp.Header.Del("Content-Length")
	return resp, nil
}
