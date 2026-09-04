package service

import (
	"strings"

	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/types"
)

// ChannelErrorCategory classifies relay errors for disable / notify decisions.
type ChannelErrorCategory int

const (
	CategorySkip ChannelErrorCategory = iota
	CategoryUpstreamRecharge
	CategoryDisableImmediate
	CategoryDisableWindow
	CategoryRateLimitWindow // 429 codex cooldown — higher threshold before disable
)

var (
	upstreamRechargeHighConfidence = []string{
		"余额不足",
		"账户余额不足",
		"insufficient balance",
		"insufficient_balance",
		"balance is insufficient",
		"balance not enough",
		"credit balance is too low",
		"your credit balance is too low",
		"no credits",
		"out of credits",
		"exceeded your current quota",
	}

	upstreamRechargeMediumConfidence = []string{
		"remaining upstream balance",
		"upstream remaining balance",
		"account remaining balance",
	}

	distributorNoAvailableMarkers = []string{
		"no available channel for model",
		"无可用渠道",
	}

	platformUserQuotaMarkers = []string{
		"用户额度不足",
		"预扣费额度失败",
		"用户剩余额度",
		"订阅额度不足",
		"subscription quota insufficient",
		"insufficient user quota",
		"token quota is not enough",
		"user quota is not enough",
	}

	rateLimitCooldownMarkers = []string{
		"cooling down",
		"are cooling down",
		// Provider-side concurrency/queue saturation. These are distinct from
		// user quota errors and should enter the channel rate-limit window.
		"concurrency limit exceeded",
		"too many pending requests",
		"upstream overload",
		"rate_limit_error",
	}
)

func ClassifyChannelError(err *types.NewAPIError) ChannelErrorCategory {
	if err == nil {
		return CategorySkip
	}

	code := err.GetErrorCode()
	if code == types.ErrorCodeInsufficientUserQuota ||
		code == types.ErrorCodePreConsumeTokenQuotaFailed {
		return CategorySkip
	}

	if types.IsImageGenerationTimeoutError(err) {
		return CategorySkip
	}

	msg := strings.ToLower(err.Error())
	if isPlatformUserQuotaError(err) {
		return CategorySkip
	}

	for _, m := range distributorNoAvailableMarkers {
		if strings.Contains(msg, m) {
			// A distributor response can mean that this channel's upstream
			// account pool is exhausted. Let the caller run the recovery probe;
			// a successful probe prevents an unnecessary model disable.
			return CategoryDisableWindow
		}
	}

	if types.IsChannelError(err) {
		switch code {
		case types.ErrorCodeChannelInvalidKey, types.ErrorCodeChannelNoAvailableKey:
			return CategoryDisableImmediate
		}
	}
	// The retry path already treats an upstream model_not_found response as a
	// channel-side capability failure. Keep the health path consistent so a
	// broken advertised model is removed from this channel's routing abilities
	// instead of being selected indefinitely. The distributor "no available
	// channel" case is handled above and still requires a confirmation probe.
	if code == types.ErrorCodeModelNotFound || isUpstreamModelUnavailableError(err) {
		return CategoryDisableImmediate
	}

	if err.StatusCode == 401 {
		if strings.Contains(msg, "invalid") ||
			strings.Contains(msg, "unauthorized") ||
			strings.Contains(msg, "authentication") {
			return CategoryDisableImmediate
		}
	}
	if isModelAccessForbiddenError(err) {
		return CategoryDisableImmediate
	}

	if isUpstreamRechargeError(err) {
		return CategoryUpstreamRecharge
	}

	if operation_setting.ShouldDisableByStatusCode(err.StatusCode) {
		// Legacy keyword/status path for configured codes (default 401 handled above).
		return CategoryDisableImmediate
	}

	lowerMessage := strings.ToLower(err.Error())
	if search, _ := AcSearch(lowerMessage, operation_setting.AutomaticDisableKeywords, true); search {
		// These are legacy automatic-disable keywords, not proof that the
		// upstream account is out of credit. Explicit balance/quota messages
		// have already been classified by isUpstreamRechargeError above.
		return CategoryDisableImmediate
	}

	if isRateLimitCooldown(err) {
		return CategoryRateLimitWindow
	}

	if isWindowFault(err) {
		return CategoryDisableWindow
	}

	return CategorySkip
}

func isUpstreamRechargeError(err *types.NewAPIError) bool {
	msg := err.Error()
	lower := strings.ToLower(msg)
	if isHighConfidenceUpstreamRechargeMessage(lower) {
		return true
	}
	hits := 0
	for _, kw := range upstreamRechargeMediumConfidence {
		if strings.Contains(msg, kw) {
			hits++
		}
	}
	return hits >= 1 && (strings.Contains(msg, "剩余额度") || strings.Contains(lower, "403"))
}

func isHighConfidenceUpstreamRechargeMessage(lowerMessage string) bool {
	for _, kw := range upstreamRechargeHighConfidence {
		if strings.Contains(lowerMessage, strings.ToLower(kw)) {
			return true
		}
	}
	if strings.Contains(lowerMessage, "model-level budget exceeded") {
		return true
	}
	hasVirtualKey := strings.Contains(lowerMessage, "virtual key") || strings.Contains(lowerMessage, "virtual_key")
	return hasVirtualKey && strings.Contains(lowerMessage, "budget exceeded")
}

func isPlatformUserQuotaError(err *types.NewAPIError) bool {
	if err == nil {
		return false
	}
	code := err.GetErrorCode()
	if code == types.ErrorCodeInsufficientUserQuota ||
		code == types.ErrorCodePreConsumeTokenQuotaFailed {
		return true
	}
	msg := strings.ToLower(err.Error())
	for _, kw := range platformUserQuotaMarkers {
		if strings.Contains(msg, strings.ToLower(kw)) {
			return true
		}
	}
	return false
}

func isModelAccessForbiddenError(err *types.NewAPIError) bool {
	if err == nil || (err.StatusCode != 403 && err.StatusCode != 404) {
		return false
	}
	msg := strings.ToLower(err.Error())
	markers := []string{
		"not found the model",
		"无权访问模型",
		"no access to model",
		"not authorized to access model",
		"not authorised to access model",
		"does not have access to model",
		"don't have access to model",
		"do not have access to model",
		"model access denied",
	}
	for _, marker := range markers {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}

func isUpstreamModelUnavailableError(err *types.NewAPIError) bool {
	if err == nil || (err.StatusCode != 400 && err.StatusCode != 404) {
		return false
	}
	msg := strings.ToLower(err.Error())
	markers := []string{
		"unknown provider for model",
		"unknown model provider",
	}
	for _, marker := range markers {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}

func isRateLimitCooldown(err *types.NewAPIError) bool {
	if err.StatusCode != 429 {
		return false
	}
	lower := strings.ToLower(err.Error())
	for _, m := range rateLimitCooldownMarkers {
		if strings.Contains(lower, m) {
			return true
		}
	}
	return false
}

func isWindowFault(err *types.NewAPIError) bool {
	if types.IsSkipRetryError(err) {
		return false
	}
	switch err.StatusCode {
	case 502, 503, 504, 524:
		return true
	}
	lower := strings.ToLower(err.Error())
	faultMarkers := []string{
		"timeout", "timed out", "bad gateway", "bad response status code",
		"connection reset", "connection refused", "upstream",
	}
	for _, m := range faultMarkers {
		if strings.Contains(lower, m) {
			return true
		}
	}
	return false
}

func IsHighConfidenceRecharge(err *types.NewAPIError) bool {
	if err == nil {
		return false
	}
	return isHighConfidenceUpstreamRechargeMessage(strings.ToLower(err.Error()))
}
