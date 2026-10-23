package service

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
)

// Ark's resource Error describes moderation; Result.Error without a resource
// identifier instead describes a rejected Action, even when HTTP is 200.
func beeNexProtocolError(action string, status int, envelope map[string]any, body []byte, headers http.Header, secrets ...string) error {
	metadata, _ := envelope["ResponseMetadata"].(map[string]any)
	result, _ := envelope["Result"].(map[string]any)
	detail, _ := metadata["Error"].(map[string]any)
	meaningful := func(value map[string]any) bool {
		return seedanceString(value, "Code", "code", "Message", "message") != ""
	}
	if !meaningful(detail) {
		detail, _ = envelope["error"].(map[string]any)
	}
	if !meaningful(detail) && !(action == "GetAsset" && seedanceString(result, "Id", "id") != "" && seedanceString(result, "Status", "status") != "") {
		detail, _ = result["Error"].(map[string]any)
	}
	if status >= 200 && status < 300 && !meaningful(detail) && (envelope["error"] == nil || envelope["error"] == "") {
		return nil
	}
	code := seedanceString(detail, "Code", "code")
	message := seedanceString(detail, "Message", "message")
	mapped, publicCode := status, "material_request_rejected"
	if mapped < 400 {
		mapped = http.StatusBadRequest
	}
	if strings.Contains(strings.ToLower(code), "limit") || strings.Contains(strings.ToLower(code), "throttl") {
		mapped = http.StatusTooManyRequests
	}
	internal := strings.EqualFold(code, "InternalError") || strings.HasPrefix(strings.ToLower(code), "internalerror.")
	if status >= 500 || status == 401 || status == 403 || internal {
		mapped = http.StatusBadGateway
		publicCode = "submission_unknown"
		if action == "GetAsset" || action == "GetAssetGroup" || action == "ListAssets" || action == "ListAssetGroups" {
			publicCode = "material_query_unavailable"
		}
	}
	if message == "" {
		message = "Material library operation was rejected"
	}
	message = fmt.Sprintf("%s: %s %s", action, code, message)
	if requestID := seedanceString(metadata, "RequestId"); requestID != "" {
		message += " (request_id=" + requestID + ")"
	}
	failure := &SeedanceAPIError{Status: mapped, Message: common.SanitizeUpstreamErrorText(message, secrets...), Code: publicCode}
	failure.UpstreamError = common.NewPublicUpstreamError(status, body, headers, secrets...)
	return failure
}
