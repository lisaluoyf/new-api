package controller

import "strings"

// Classify explicit provider policy rejections, not ordinary 400/403 errors or
// generic upstream_text_reply responses. Never send rejected content to another
// provider through retry, official fallback, or the free-model candidate plan.
func isContentSafetyRejection(code, errorType, message string) bool {
	for _, value := range []string{code, errorType} {
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "content_policy_violation", "content_filter", "content_filter_error",
			"content_filter_blocked", "safety_violation", "safety_rejection",
			"moderation_blocked", "moderation_rejected", "responsibleaipolicyviolation", "safety",
			"image_safety_error", "content_policy_rejection", "content_moderation",
			"data_inspection_failed", "sensitive_words_detected", "violation_fee.grok.csam",
			"risk_control", "risk_control_blocked":
			return true
		}
	}
	message = strings.ToLower(message)
	for _, marker := range []string{
		"content policy violation", "violates our content policy", "violates the content policy",
		"violates our safety policy", "violated our safety policy", "rejected by the safety system",
		"rejected as a result of our safety system", "blocked due to safety",
		"blocked by our safety system", "blocked by the safety system", "blocked by content filter",
		"blocked by the content filter", "rejected by content moderation",
		"触发风控", "触发了风控", "触发内容安全", "内容违反了安全政策", "风控拦截", "触发了内容安全", "违反内容政策", "违反了内容政策",
		"违反了关于暴力内容的防护限制", "内容安全审核未通过", "未通过内容安全审核",
	} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}
