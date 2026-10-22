package types

import "strings"

// RelayErrorCategory separates request/account errors from upstream availability.
// This metadata supplements the original status, code and error details.
func RelayErrorCategory(code, message string, status int) string {
	s := strings.ToLower(code + " " + message)
	switch {
	case status == 499 || strings.Contains(s, "client_cancel") || strings.Contains(s, "client_gone") || strings.Contains(s, "context canceled"):
		return "client_disconnect"
	case strings.Contains(s, "insufficient_user_quota") || strings.Contains(s, "pre_consume_token_quota_failed") || strings.Contains(s, "gpt_subscription_rolling_limit") || strings.Contains(s, "gpt pass") || strings.Contains(s, "额度已用尽"):
		return "quota_limit"
	case IsContentSafetyRejection(code, "", message) || strings.Contains(s, "content_policy") || strings.Contains(s, "content_safety") || strings.Contains(s, "内容审计") || strings.Contains(s, "cybersecurity risk") || strings.Contains(s, "prompt_blocked") || strings.Contains(s, "sensitive_words"):
		return "content_policy"
	case status == 400 || strings.Contains(s, "unsupported_reasoning_effort") || strings.Contains(s, "invalid_request"):
		return "request_parameter"
	case status == 429:
		return "upstream_rate_limit"
	case status >= 500:
		return "upstream_failure"
	default:
		return "other"
	}
}
