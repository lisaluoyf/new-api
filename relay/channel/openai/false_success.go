package openai

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
)

const (
	falseSuccessTriggerResponseError   = "response_error"
	falseSuccessTriggerFailedStatus    = "response_failed_status"
	falseSuccessTriggerEmptyResponse   = "empty_response"
	falseSuccessTriggerEmptyStream     = "empty_stream"
	falseSuccessTriggerMissingTerminal = "missing_terminal_event"
	falseSuccessTriggerAbnormalStream  = "abnormal_stream_end"
)

func markResponsesFalseSuccess(
	relayErr *types.NewAPIError,
	trigger string,
	upstreamHTTPStatus int,
	stream bool,
	eventType string,
	response *dto.OpenAIResponsesResponse,
	upstreamErr *types.OpenAIError,
	streamStatus *relaycommon.StreamStatus,
	terminalEvent bool,
	usableOutput bool,
	rawResponse string,
) *types.NewAPIError {
	if relayErr == nil || upstreamHTTPStatus < http.StatusOK || upstreamHTTPStatus >= http.StatusMultipleChoices {
		return relayErr
	}
	diagnostic := types.UpstreamFalseSuccessDiagnostic{
		Trigger:            trigger,
		UpstreamHTTPStatus: upstreamHTTPStatus,
		Stream:             stream,
		EventType:          eventType,
		ResponseStatus:     responsesResponseStatus(response),
		TerminalEvent:      terminalEvent,
		UsableOutput:       usableOutput,
		RawResponse:        rawResponse,
	}
	if upstreamErr != nil {
		diagnostic.ErrorType = upstreamErr.Type
		diagnostic.ErrorCode = falseSuccessErrorCode(upstreamErr.Code)
		diagnostic.ErrorMessage = upstreamErr.Message
	}
	if streamStatus != nil {
		diagnostic.StreamEndReason = string(streamStatus.EndReason)
	}
	relayErr.SetUpstreamFalseSuccess(diagnostic)
	return relayErr
}

func falseSuccessErrorCode(code any) string {
	if code == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(code))
}

func falseSuccessRawFrames(frames []string) string {
	if len(frames) == 0 {
		return ""
	}
	if len(frames) == 1 {
		return frames[0]
	}
	return frames[0] + "\n...\n" + frames[len(frames)-1]
}

func falseSuccessStreamTrigger(status *relaycommon.StreamStatus, terminalEvent, usableOutput bool) string {
	if terminalEvent && !usableOutput {
		return falseSuccessTriggerEmptyStream
	}
	if status != nil && !terminalEvent &&
		(status.EndReason == relaycommon.StreamEndReasonDone || status.EndReason == relaycommon.StreamEndReasonEOF) {
		return falseSuccessTriggerMissingTerminal
	}
	return falseSuccessTriggerAbnormalStream
}
