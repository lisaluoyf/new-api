package service

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

func IsCtaiSeedanceBaseURL(base string) bool {
	u, err := url.Parse(base)
	return err == nil && u.Scheme == "https" && u.Host == "ai.ctaigw.cn" && u.User == nil && (u.Path == "" || u.Path == "/") && u.RawQuery == "" && u.Fragment == ""
}
func isCtaiSeedanceChannel(ch *model.Channel) bool {
	return ch != nil && ch.Id == 298 && ch.Type == constant.ChannelTypeDoubaoVideo && IsCtaiSeedanceBaseURL(ch.GetBaseURL())
}
func validCtaiAssetID(id string) bool {
	if id == "" || len(id) > 128 {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

// CTAI owns its upstream asset group. APIMaster/RomaAPI groups are local owner
// mappings only; no undocumented upstream group operation is issued.
func ctaiAssetRequest(ctx context.Context, ch *model.Channel, key, action string, fields map[string]any, exposeReviewError bool) (map[string]any, error) {
	if !isCtaiSeedanceChannel(ch) {
		return nil, seedanceError(503, "Invalid CTAI media gateway")
	}
	result := func(data map[string]any) (map[string]any, error) { return map[string]any{"Result": data}, nil }
	switch action {
	case "CreateAssetGroup":
		name := seedanceString(fields, "Name")
		if name == "" {
			return nil, seedanceError(400, "Invalid local material group")
		}
		return result(map[string]any{"Id": "ctai-local-" + SeedanceKeyFingerprint(key + ":" + name)[:32], "Status": "Active"})
	case "GetAssetGroup", "UpdateAssetGroup", "DeleteAssetGroup":
		return result(map[string]any{"Id": seedanceString(fields, "Id"), "Status": "Active"})
	}
	method, path := http.MethodGet, "/v1/api/assets/"
	var body io.Reader
	switch action {
	case "CreateAsset":
		kind := seedanceString(fields, "AssetType")
		if kind != "Image" && kind != "Video" && kind != "Audio" {
			return nil, seedanceError(400, "Unsupported material type")
		}
		raw, err := common.Marshal(map[string]any{"url": fields["URL"], "asset_type": kind, "name": fields["Name"]})
		if err != nil {
			return nil, err
		}
		method, path, body = http.MethodPost, path+"upload", bytes.NewReader(raw)
	case "GetAsset", "DeleteAsset":
		id := seedanceString(fields, "Id")
		if !validCtaiAssetID(id) {
			return nil, seedanceError(400, "Invalid material identifier")
		}
		path += url.PathEscape(id)
		if action == "DeleteAsset" {
			method = http.MethodDelete
		}
	default:
		return nil, seedanceError(400, "This upstream does not support this material operation")
	}
	req, err := http.NewRequestWithContext(ctx, method, "https://ai.ctaigw.cn"+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	// CTAI waits indefinitely for a JSON body on bodyless GET/DELETE requests.
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client := &http.Client{Timeout: 45 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return nil, seedanceError(502, "Material request could not be confirmed; query existing resources before retrying", "submission_unknown")
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, (4<<20)+1))
	var envelope map[string]any
	if err != nil || len(raw) > 4<<20 || common.Unmarshal(raw, &envelope) != nil || envelope == nil {
		return nil, seedanceError(502, "Invalid material response", "submission_unknown")
	}
	code, hasCode := envelope["code"]
	failed := hasCode && code != float64(0) && code != "0"
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || failed || envelope["error"] != nil {
		status, errorCode := resp.StatusCode, "media_library_error"
		if status < 400 || status >= 500 {
			status, errorCode = 502, "submission_unknown"
		}
		message := "CTAI material request failed"
		if exposeReviewError && resp.StatusCode >= 400 && resp.StatusCode < 500 {
			detail, _ := envelope["error"].(map[string]any)
			reason := seedanceString(detail, "message", "Message")
			if reason == "" {
				reason = seedanceString(envelope, "message", "Message")
			}
			if reason != "" {
				message = sanitizeTaskFailure(reason, append([]string{key, seedanceString(fields, "URL"), seedanceString(fields, "Id")}, ch.GetKeys()...)...)
			}
			errorCode = "invalid_asset_material"
		}
		return nil, seedanceError(status, message, errorCode)
	}
	data, ok := envelope["data"].(map[string]any)
	if !ok {
		if action == "DeleteAsset" {
			return result(map[string]any{})
		}
		return nil, seedanceError(502, "Material response has no result", "submission_unknown")
	}
	// A numeric string is an opaque upstream identifier; never parse it as a float.
	if action == "CreateAsset" && !validCtaiAssetID(seedanceString(data, "Id", "id")) {
		return nil, seedanceError(502, "Material response has no identifier", "submission_unknown")
	}
	if action == "GetAsset" {
		// Preserve known rejection reasons for the common review state machine.
		if strings.EqualFold(seedanceString(data, "Status", "status"), "Failed") && data["Error"] == nil {
			reason := seedanceString(data, "FailReason", "fail_reason", "FailureReason", "Reason", "message")
			if reason != "" {
				data["Error"] = map[string]any{"Message": sanitizeTaskFailure(reason, key, seedanceString(fields, "Id"))}
			}
		}
	}
	return result(data)
}

// As on the existing admin-pinned video route, permit an explicitly pinned
// enabled CTAI channel for an administrator. Normal API users retain all group
// restrictions. Client/provider policies and model allowlists still apply.
func ctaiMaterialAdminPinned(c *gin.Context, ch *model.Channel, name string) bool {
	if !isCtaiSeedanceChannel(ch) || ch.Status != common.ChannelStatusEnabled {
		return false
	}
	pinned, ok := common.GetContextKey(c, constant.ContextKeyTokenSpecificChannelId)
	if !ok || pinned != "298" || !model.IsAdmin(c.GetInt("id")) || !ProviderChannelAllowed(ProviderPolicyForModel(c, name), ch.Id) || ValidateChannelClientPolicy(c, ch, name) != nil || ValidateSeedanceModelAccess(c, name) != nil {
		return false
	}
	for _, m := range ch.GetModels() {
		if m == name {
			return true
		}
	}
	return false
}
