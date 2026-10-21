package service

import (
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func identityTestContext() *gin.Context {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set("channel_name", "HiddenRelay-video")
	c.Set("base_url", "https://api.private-route.ai")
	c.Set("original_model", "public-video")
	c.Set("public_error_private_model", "private-deployment-7")
	return c
}
func TestPublicRelayViewHidesIdentityInEveryPublicField(t *testing.T) {
	c := identityTestContext()
	originalMessage := "HiddenRelay: private-deployment-7 on api.private-route.ai rejected image[0]; expected image/jpeg, actual video/mp4"
	failure := types.WithOpenAIError(types.OpenAIError{Code: "HiddenRelay.InvalidImage", Type: "HiddenRelay.ValidationError", Message: originalMessage, Param: "image[0]", Metadata: []byte(`{"vendor":"HiddenRelay","reason":"HiddenRelay expected image/jpeg"}`)}, 422)
	failure.UpstreamError = common.NewPublicUpstreamError(422, []byte(`{"provider":"OtherRelay","error":{"code":"OtherRelay.InvalidImage","message":"OtherRelay rejected image[0]; expected JPEG","details":{"expected":"image/jpeg","actual":"video/mp4"}}}`), nil)
	rawCode, rawError := failure.GetErrorCode(), failure.Error()
	public := PublicRelayErrorView(c, failure)
	for _, value := range []any{public.ToOpenAIError(), public.ToClaudeError()} {
		encoded, err := common.Marshal(value)
		require.NoError(t, err)
		for _, identity := range []string{"hiddenrelay", "otherrelay", "private-deployment", "private-route.ai"} {
			require.NotContains(t, strings.ToLower(string(encoded)), identity)
		}
		require.Contains(t, string(encoded), "image[0]")
		require.Contains(t, string(encoded), "image/jpeg")
	}
	require.Equal(t, rawCode, failure.GetErrorCode(), "retry/billing error code must remain original")
	require.Equal(t, rawError, failure.Error(), "health error reason must remain original")
}
func TestPublicTaskViewDoesNotMutateRetryOrBillingInput(t *testing.T) {
	c := identityTestContext()
	failure := &dto.TaskError{Code: "HiddenRelay.InvalidImage", Message: "HiddenRelay rejected image[0]", LocalError: true, StatusCode: 400, Error: errors.New("original health reason"), Data: map[string]any{"provider": "HiddenRelay", "message": "HiddenRelay requires image/jpeg"}}
	public := PublicTaskErrorView(c, failure)
	encoded, err := common.Marshal(public)
	require.NoError(t, err)
	require.NotContains(t, strings.ToLower(string(encoded)), "hiddenrelay")
	require.Contains(t, string(encoded), "image[0]")
	require.Contains(t, string(encoded), "image/jpeg")
	require.Equal(t, "HiddenRelay.InvalidImage", failure.Code)
	require.Equal(t, "HiddenRelay rejected image[0]", failure.Message)
	require.Equal(t, "original health reason", failure.Error.Error())
	require.True(t, public.LocalError)
	require.Equal(t, 400, public.StatusCode)
}
