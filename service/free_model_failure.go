package service

import (
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/types"
)

type FreeModelFailureDisposition struct {
	Transient bool
	Permanent bool
	Reason    string
}

func ClassifyFreeModelFailure(err *types.NewAPIError) FreeModelFailureDisposition {
	if err == nil {
		return FreeModelFailureDisposition{}
	}
	marker := strings.ToLower(string(err.GetErrorCode()) + " " + string(err.GetErrorType()) + " " + err.Error())
	for _, permanent := range []string{
		"model is unavailable for free",
		"model not found",
		"model_not_found",
		"no endpoints found",
		"not supported by any configured account",
	} {
		if strings.Contains(marker, permanent) {
			return FreeModelFailureDisposition{Permanent: true, Reason: "upstream_model_unavailable"}
		}
	}
	switch err.GetErrorCode() {
	case types.ErrorCode("invalid_json"), types.ErrorCode("invalid_tool_arguments"), types.ErrorCode("empty_response"), types.ErrorCode("missing_required_tool_call"), types.ErrorCode("invalid_finish_reason"), types.ErrorCode("schema_validation_failed"):
		return FreeModelFailureDisposition{Transient: true, Reason: "invalid_upstream_response"}
	}
	for _, temporary := range []string{"rate_limit", "too_many_requests", "temporarily_unavailable", "temporary_unavailable", "overloaded", "timeout", "context deadline exceeded", "connection reset", "unexpected eof"} {
		if strings.Contains(marker, temporary) {
			return FreeModelFailureDisposition{Transient: true, Reason: "temporary_upstream_failure"}
		}
	}
	status := err.StatusCode
	if status == http.StatusRequestTimeout || status == http.StatusTooManyRequests || status == 524 || status >= 500 || status <= 0 {
		return FreeModelFailureDisposition{Transient: true, Reason: "temporary_upstream_failure"}
	}
	return FreeModelFailureDisposition{Reason: "non_retryable_request_error"}
}
