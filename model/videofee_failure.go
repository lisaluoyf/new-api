package model

import (
	"regexp"
	"strings"

	"github.com/QuantumNous/new-api/common"
)

var videoFeeMetadataLeadingComma = regexp.MustCompile(`("ResponseMetadata"\s*:\s*\{)\s*,`)

// VideoFeeFailureDetails reads only the provider's failure fields. Some late
// material errors contain a redacted Volcengine envelope with a leading comma.
// Repair that known wrapper only; never expose diagnostic siblings or guess an
// official error code from the prose.
func VideoFeeFailureDetails(raw []byte) (string, string) {
	var envelope map[string]any
	if common.Unmarshal(raw, &envelope) != nil {
		return "", ""
	}
	data := envelope
	if nested, ok := envelope["data"].(map[string]any); ok {
		data = nested
	}
	status, _ := data["status"].(string)
	switch strings.ToUpper(status) {
	case "FAILURE", "FAILED", "CANCELLED", "CANCELED":
	default:
		return "", ""
	}
	code, _ := data["error_code"].(string)
	code = PublicVideoErrorCode(code)
	reason, _ := data["fail_reason"].(string)
	reason = strings.TrimSpace(reason)
	if nestedCode, nestedMessage := findPublicVideoError(envelope, 0); nestedCode != "" && nestedCode != "task_failed" && nestedCode != "generation_failed" {
		code = nestedCode
		if !IsGenericTaskFailureReason(nestedMessage) {
			reason = nestedMessage
		}
	}
	if strings.HasPrefix(reason, "upstream material api failed: volc api http ") {
		if index := strings.Index(reason, "{"); index >= 0 {
			body := videoFeeMetadataLeadingComma.ReplaceAllString(reason[index:], "${1}")
			var wrapped struct {
				Metadata struct {
					Error struct{ Code, Message string }
				} `json:"ResponseMetadata"`
			}
			if common.UnmarshalJsonStr(body, &wrapped) == nil {
				if official := PublicVideoErrorCode(wrapped.Metadata.Error.Code); official != "" {
					code = official
				}
				reason = wrapped.Metadata.Error.Message
			} else {
				reason = ""
			}
		}
	}
	if explicit, message := taskFailureTextDetails(reason); explicit != "" {
		code, reason = explicit, message
	}
	if IsGenericTaskFailureReason(reason) {
		reason = ""
	}
	if code == "" && reason != "" {
		code = "task_failed"
	}
	return code, reason
}
