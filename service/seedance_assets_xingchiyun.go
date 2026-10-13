package service

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/system_setting"
)

func isXingchiyunSeedanceChannel(ch *model.Channel) bool {
	if ch == nil || ch.Type != constant.ChannelTypeDoubaoVideo {
		return false
	}
	u, err := url.Parse(ch.GetBaseURL())
	return err == nil && u.Scheme == "https" && u.Hostname() == "api.xingchiyun.com" && u.User == nil
}

func supportsSeedanceH5(ch *model.Channel) bool {
	return isBytePlusSeedanceChannel(ch) || isXingchiyunSeedanceChannel(ch)
}

// Action management shares Ark payloads, but gateway authentication must never
// send official control-plane AK/SK to the gateway.
func seedanceActionRequest(ctx context.Context, resource *model.SeedanceResource, action string, payload map[string]any) (map[string]any, error) {
	ch, err := model.GetChannelById(resource.ChannelID, true)
	if err != nil || ch == nil || ch.Status != common.ChannelStatusEnabled {
		return nil, seedanceError(503, "Media library is temporarily unavailable")
	}
	if isBeeNexSeedance25Channel(ch) && resource.Model == "seedance-2.5" {
		return beeNexAssetRequest(ctx, resource, action, payload)
	}
	if !isXingchiyunSeedanceChannel(ch) {
		return bytePlusAssetRequest(ctx, resource, action, payload)
	}
	key, _, err := SeedanceResourceKey(ch, resource.KeyFingerprint)
	if err != nil {
		return nil, err
	}
	fields := make(map[string]any, len(payload)+1)
	for k, v := range payload {
		fields[k] = v
	}
	fields["ProjectName"] = "default"
	delete(fields, "CallbackURL") // The gateway hosts and configures its own callback.
	raw, err := common.Marshal(fields)
	if err != nil {
		return nil, err
	}
	u, err := url.Parse(ch.GetBaseURL())
	if err != nil {
		return nil, seedanceError(502, "Invalid media library gateway")
	}
	u.Path, u.RawPath, u.RawQuery, u.Fragment = "/v1/seedance", "", url.Values{"Action": {action}, "Version": {"2024-01-01"}}.Encode(), ""
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(raw))
	if err != nil {
		return nil, seedanceError(502, "Unable to contact media library")
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 45 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(req)
	if err != nil {
		return nil, seedanceError(502, "Media library request failed; query existing resources before submitting again")
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, (4<<20)+1))
	var envelope map[string]any
	if err != nil || len(body) > 4<<20 || common.Unmarshal(body, &envelope) != nil || envelope == nil {
		return nil, seedanceError(502, "Invalid media library response")
	}
	metadata, _ := envelope["ResponseMetadata"].(map[string]any)
	detail, failed := metadata["Error"].(map[string]any)
	if response.StatusCode < 200 || response.StatusCode >= 300 || failed || envelope["error"] != nil {
		// Only this documented token response is a waiting state. Other 404s
		// remain errors; never disclose a token embedded in upstream errors.
		if action == "GetVisualValidateResult" {
			token := seedanceString(fields, "BytedToken")
			if response.StatusCode == 404 && token != "" && seedanceString(detail, "Code") == "NotFound."+token {
				return nil, seedanceError(404, "Owner verification is not yet confirmed", "verification_pending")
			}
			message := seedanceString(detail, "Message")
			if message == "" {
				e, _ := envelope["error"].(map[string]any)
				message = seedanceString(e, "message", "Message")
			}
			if response.StatusCode == 400 && strings.TrimSpace(message) == "validation is not pending or successful" {
				return nil, seedanceError(400, "Owner verification failed or expired", "verification_failed_or_expired")
			}
		}
		status := response.StatusCode
		if status < 400 {
			status = 502
		}
		return nil, seedanceError(status, "Media library request could not be confirmed; query existing resources before retrying")
	}
	return envelope, nil
}

func validXingchiyunVerificationToken(token string) bool {
	return token != "" && len(token) <= 255 && strings.IndexFunc(token, unicode.IsSpace) < 0
}

func seedanceGatewayPreviewData(asset *model.SeedanceResource, result map[string]any) string {
	ch, _ := model.GetChannelById(asset.ChannelID, true)
	if !isXingchiyunSeedanceChannel(ch) {
		return asset.ResultData
	}
	preview := seedanceString(result, "URL")
	u, err := url.Parse(preview)
	if err != nil || u.Hostname() == "" || u.User != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return ""
	}
	encoded, _ := common.Marshal(map[string]any{"preview_url": preview})
	return string(encoded)
}

func SeedanceAssetPreviewSource(asset *model.SeedanceResource) string {
	var data map[string]any
	if asset == nil || common.UnmarshalJsonStr(asset.ResultData, &data) != nil {
		return ""
	}
	return seedanceString(data, "preview_url")
}

func seedanceGatewayPublicPreview(asset *model.SeedanceResource) string {
	if SeedanceAssetPreviewSource(asset) == "" {
		return ""
	}
	return strings.TrimRight(system_setting.ServerAddress, "/") + seedanceLibraryPath + "/assets/" + asset.ID + "/preview"
}
