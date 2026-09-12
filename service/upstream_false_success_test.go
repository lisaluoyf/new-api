package service

import (
	"testing"

	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAppendUpstreamFalseSuccessSummary(t *testing.T) {
	c, _ := gin.CreateTestContext(nil)
	RecordUpstreamFalseSuccessAttempt(c, &types.UpstreamFalseSuccessDiagnostic{Trigger: "response_error", ErrorCode: "server_is_overloaded"})
	RecordUpstreamFalseSuccessAttempt(c, &types.UpstreamFalseSuccessDiagnostic{Trigger: "response_error", ErrorCode: "server_is_overloaded"})
	RecordUpstreamFalseSuccessAttempt(c, &types.UpstreamFalseSuccessDiagnostic{Trigger: "empty_stream"})

	adminInfo := map[string]interface{}{}
	AppendUpstreamFalseSuccessSummary(c, adminInfo)

	summary, ok := adminInfo["upstream_false_success"].(map[string]interface{})
	require.True(t, ok)
	require.Equal(t, true, summary["triggered"])
	require.Equal(t, 3, summary["count"])
	require.Equal(t, []string{"response_error", "empty_stream"}, summary["triggers"])
	require.Equal(t, []string{"server_is_overloaded"}, summary["error_codes"])

	count, triggers, errorCodes, latest, ok := GetUpstreamFalseSuccessSummary(c)
	require.True(t, ok)
	require.Equal(t, 3, count)
	require.Equal(t, []string{"response_error", "empty_stream"}, triggers)
	require.Equal(t, []string{"server_is_overloaded"}, errorCodes)
	require.Equal(t, "empty_stream", latest.Trigger)
}
