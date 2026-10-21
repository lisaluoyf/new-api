package service

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/stretchr/testify/require"
)

type diagnosticSDKError struct{ message, requestID string }

func (e *diagnosticSDKError) Error() string            { return "operation HiddenSDK Runtime: " + e.message }
func (e *diagnosticSDKError) Service() string          { return "HiddenSDK Runtime" }
func (e *diagnosticSDKError) ErrorCode() string        { return "HiddenRelay.InvalidImage" }
func (e *diagnosticSDKError) ErrorMessage() string     { return e.message }
func (e *diagnosticSDKError) HTTPStatusCode() int      { return 422 }
func (e *diagnosticSDKError) ServiceRequestID() string { return e.requestID }

func TestSDKTypedDiagnosticSurvivesWrappedErrorsAndRedactsSecrets(t *testing.T) {
	c := identityTestContext()
	message := "HiddenRelay expected image/jpeg for image[0]; " + strings.Repeat("reason ", 200) + "diagnostic-end secret-part"
	cause := fmt.Errorf("wrapped SDK cause: %w", &diagnosticSDKError{message, "opaque-request-7"})
	failure := types.NewOpenAIError(cause, types.ErrorCodeAwsInvokeError, 422)
	beforeCode, beforeMessage := failure.GetErrorCode(), failure.Error()
	AttachSDKUpstreamError(c, failure, cause, "AWS", "BedrockRuntime", "secret-part")
	require.Equal(t, 422, failure.UpstreamError.StatusCode)
	require.Equal(t, "opaque-request-7", failure.UpstreamError.RequestID)
	wire, err := common.Marshal(PublicRelayErrorView(c, failure).ToOpenAIError())
	require.NoError(t, err)
	for _, secret := range []string{"HiddenRelay", "BedrockRuntime", "HiddenSDK Runtime", "secret-part"} {
		require.NotContains(t, string(wire), secret)
	}
	require.Contains(t, string(wire), "image[0]")
	require.Contains(t, string(wire), "diagnostic-end")
	require.Equal(t, beforeCode, failure.GetErrorCode())
	require.Equal(t, beforeMessage, failure.Error())
}

func TestSDKHTTPRetryDoesNotReusePreviousAttempt(t *testing.T) {
	c := identityTestContext()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(422)
		_, _ = io.WriteString(w, `{"error":{"message":"previous-attempt-only"}}`)
	}))
	client := SDKErrorHTTPClient(c, server.Client())
	response, err := client.Get(server.URL)
	require.NoError(t, err)
	_, err = io.ReadAll(response.Body)
	require.NoError(t, err)
	response.Body.Close()
	require.NotNil(t, CapturedUpstreamError(c))
	server.Close()
	_, err = client.Get(server.URL)
	require.Error(t, err)
	failure := types.NewOpenAIError(err, types.ErrorCodeDoRequestFailed, 502)
	AttachSDKUpstreamError(c, failure, err)
	wire, _ := common.Marshal(failure.UpstreamError)
	require.NotContains(t, string(wire), "previous-attempt-only")
	require.Zero(t, failure.UpstreamError.StatusCode, "do not invent an upstream HTTP status for transport failures")
}

func TestStreamErrorFrameRetainsDetailsAndIgnoresBusinessFrames(t *testing.T) {
	c := identityTestContext()
	CaptureUpstreamStreamError(c, `{"type":"response.output_text.delta","delta":"business content","error":null}`, nil)
	require.Nil(t, CapturedUpstreamError(c))
	response := &http.Response{StatusCode: 200, Header: http.Header{"X-Request-Id": []string{"opaque-stream-request"}}}
	CaptureUpstreamStreamError(c, `{"type":"error","provider":"HiddenRelay","error":{"code":"HiddenRelay.InvalidImage","message":"HiddenRelay rejected image[0]: expected image/jpeg","details":{"actual":"video/mp4","api_key":"secret-part"}}}`, response)
	failure := types.NewOpenAIError(errors.New("adapter summary only"), types.ErrorCodeBadResponseBody, 502)
	public := PublicStreamError(c, failure)
	require.Equal(t, 200, public.UpstreamError.StatusCode)
	require.Equal(t, "opaque-stream-request", public.UpstreamError.RequestID)
	wire, _ := common.Marshal(public)
	require.NotContains(t, string(wire), "HiddenRelay")
	require.NotContains(t, string(wire), "secret-part")
	require.Contains(t, string(wire), "video/mp4")
	require.Contains(t, string(wire), "image[0]")
	eventDetail, _ := common.Marshal(public.UpstreamError)
	logDetail, _ := common.Marshal(failure.UpstreamError)
	require.JSONEq(t, string(eventDetail), string(logDetail), "terminal event and later customer log must share a diagnostic")
	ResetUpstreamErrorCapture(c)
	require.Nil(t, CapturedUpstreamError(c))
}

func TestStreamFrameWritePolicyRedactsForwardedErrorsAndPreservesBusiness(t *testing.T) {
	c := identityTestContext()
	business := `{"type":"response.output_text.delta","delta":"HiddenRelay is user-provided content","error":null}`
	require.Equal(t, business, SafeStreamErrorFrame(c, business))
	for _, noError := range []string{"false", "0", `""`} {
		frame := strings.Replace(business, "null", noError, 1)
		require.Equal(t, frame, SafeStreamErrorFrame(c, frame))
		CaptureUpstreamStreamError(c, frame, nil)
		require.Nil(t, CapturedUpstreamError(c))
	}
	require.Equal(t, "[DONE]", SafeStreamErrorFrame(c, "[DONE]"))
	require.Contains(t, SafeStreamErrorFrame(c, `{"type":"response.failed","response":{"error":{"message":"capacity exhausted"}}}`), `"type":"response.failed"`)
	errorFrame := `{"provider":"HiddenRelay","error":{"code":"HiddenRelay.InvalidImage","message":"HiddenRelay requires image/jpeg","details":{"api_key":"secret-part","actual":"video/mp4"}}}`
	CaptureUpstreamStreamError(c, errorFrame, &http.Response{StatusCode: 200})
	wire := SafeStreamErrorFrame(c, errorFrame)
	require.NotContains(t, wire, "HiddenRelay")
	require.NotContains(t, wire, "secret-part")
	require.Contains(t, wire, "image/jpeg")
	require.Contains(t, wire, "video/mp4")
	require.Contains(t, wire, "upstream_error")
	tooBig := `{"error":{"message":"` + strings.Repeat("x", 2<<20) + `"}}`
	wire = SafeStreamErrorFrame(c, tooBig)
	require.Contains(t, wire, `"truncated":true`)
	require.Less(t, len(wire), common.MaxPublicUpstreamErrorBytes)
}
