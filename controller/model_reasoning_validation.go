package controller

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
)

// This is a request validation failure, not a failed attempt on the channel
// selected by distribution middleware. Keep it visible with channel=0, no charge,
// and no health penalty or fallback.
func recordReasoningValidationError(c *gin.Context, info *relaycommon.RelayInfo, err *types.NewAPIError) {
	setRetryDecision(c, retryDecision{Reason: "invalid_request_parameter", StatusCode: err.StatusCode, ErrorCode: string(err.GetErrorCode())})
	if !constant.ErrorLogEnabled {
		return
	}
	other := map[string]interface{}{
		"error_category": "request_parameter", "error_code": err.GetErrorCode(),
		"error_type": err.ToOpenAIError().Type, "status_code": err.StatusCode,
		"request_path": c.Request.URL.Path, "validation_stage": "before_billing",
		"admin_info": map[string]interface{}{"use_channel": []string{}, "retry_decision": retryDecisionToMap(retryDecision{Reason: "invalid_request_parameter", StatusCode: err.StatusCode, ErrorCode: string(err.GetErrorCode())})},
	}
	model.RecordErrorLog(c, c.GetInt("id"), 0, info.OriginModelName, c.GetString("token_name"), err.Error(), c.GetInt("token_id"), 0, common.GetContextKeyBool(c, constant.ContextKeyIsStream), c.GetString("group"), other)
}
