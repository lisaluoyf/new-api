package aws

import (
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	bedrocktypes "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/gin-gonic/gin"
	"github.com/pkg/errors"
)

func awsInvocationError(c *gin.Context, info *relaycommon.RelayInfo, cause error, operation string) *types.NewAPIError {
	values := append([]string{info.ApiKey, "AWS", "BedrockRuntime"}, strings.Split(info.ApiKey, "|")...)
	values = append(values, common.UpstreamIdentityValues(bedrockruntime.ServiceID, "", "", "")...)
	failure := service.AttachSDKUpstreamError(c, types.NewOpenAIError(errors.Wrap(cause, operation), types.ErrorCodeAwsInvokeError, getAwsErrorStatusCode(cause)), cause, values...)
	var streamError *bedrocktypes.ModelStreamErrorException
	if errors.As(cause, &streamError) && service.CapturedUpstreamError(c) == nil {
		// These modeled fields are absent from ErrorMessage(), and explain the real failure.
		raw, _ := common.Marshal(map[string]any{"error": map[string]any{"code": streamError.ErrorCode(), "message": streamError.ErrorMessage(), "original_message": streamError.OriginalMessage, "original_status_code": streamError.OriginalStatusCode}})
		detail := failure.UpstreamError
		failure.UpstreamError = common.NewPublicUpstreamError(detail.StatusCode, raw, http.Header{"X-Request-Id": []string{detail.RequestID}}, append(values, detail.RedactionValues()...)...)
	}
	return failure
}
