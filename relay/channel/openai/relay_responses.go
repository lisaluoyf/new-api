package openai

import (
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
)

func OaiResponsesHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	defer service.CloseResponseBodyGracefully(resp)

	// read response body
	var responsesResponse dto.OpenAIResponsesResponse
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeReadResponseBodyFailed, http.StatusInternalServerError)
	}
	err = common.Unmarshal(responseBody, &responsesResponse)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}
	if oaiError := responsesResponse.GetOpenAIError(); oaiError != nil && (oaiError.Type != "" || oaiError.Message != "" || oaiError.Code != nil) {
		return nil, types.WithOpenAIError(*oaiError, resp.StatusCode)
	}
	if responsesResponseStatus(&responsesResponse) == "failed" {
		return nil, types.NewOpenAIError(fmt.Errorf("upstream returned HTTP 200 with failed response status"), types.ErrorCodeBadResponse, http.StatusBadGateway)
	}
	if !responsesResponseHasUsableOutput(&responsesResponse) && !responsesResponseCanBeReturnedWithoutOutput(&responsesResponse) {
		return nil, types.NewOpenAIError(fmt.Errorf("upstream returned HTTP 200 without usable output"), types.ErrorCodeEmptyResponse, http.StatusBadGateway)
	}
	if validationErr := service.ValidateFreeModelResponsesResponse(c, &responsesResponse); validationErr != nil {
		return nil, validationErr
	}
	if service.IsFreeModel(info.OriginModelName) {
		responsesResponse.Model = info.OriginModelName
		responseBody, err = common.Marshal(responsesResponse)
		if err != nil {
			return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
		}
	}

	if responsesResponse.HasImageGenerationCall() {
		c.Set("image_generation_call", true)
		c.Set("image_generation_call_quality", responsesResponse.GetQuality())
		c.Set("image_generation_call_size", responsesResponse.GetSize())
	}

	// 写入新的 response body
	service.IOCopyBytesGracefully(c, resp, responseBody)

	// compute usage
	usage := dto.Usage{}
	if responsesResponse.Usage != nil {
		usage.PromptTokens = responsesResponse.Usage.InputTokens
		usage.CompletionTokens = responsesResponse.Usage.OutputTokens
		usage.TotalTokens = responsesResponse.Usage.TotalTokens
		if responsesResponse.Usage.InputTokensDetails != nil {
			usage.PromptTokensDetails.CachedTokens = responsesResponse.Usage.InputTokensDetails.CachedTokens
		}
	}
	if info == nil || info.ResponsesUsageInfo == nil || info.ResponsesUsageInfo.BuiltInTools == nil {
		return &usage, nil
	}
	// 解析 Tools 用量
	for _, tool := range responsesResponse.Tools {
		buildToolinfo, ok := info.ResponsesUsageInfo.BuiltInTools[common.Interface2String(tool["type"])]
		if !ok || buildToolinfo == nil {
			logger.LogError(c, fmt.Sprintf("BuiltInTools not found for tool type: %v", tool["type"]))
			continue
		}
		buildToolinfo.CallCount++
	}
	return &usage, nil
}

func OaiResponsesStreamHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	if resp == nil || resp.Body == nil {
		logger.LogError(c, "invalid response or response body")
		return nil, types.NewError(fmt.Errorf("invalid response"), types.ErrorCodeBadResponse)
	}

	defer service.CloseResponseBodyGracefully(resp)
	// Do not let a downstream keep-alive comment commit the HTTP response before
	// usable output arrives. Ping resumes as soon as output is committed.
	c.Set(helper.ContextKeySuppressStreamPing, true)
	defer c.Set(helper.ContextKeySuppressStreamPing, false)

	var usage = &dto.Usage{}
	var responseTextBuilder strings.Builder

	terminalFrame := false
	validOutput := false
	// Responses emits lifecycle frames before it knows whether the request will
	// produce anything. Keep those frames private until real output arrives;
	// otherwise an empty HTTP 200 stream is marked as already started and cannot
	// transparently fall back to another channel.
	var pendingFrames []string
	outputCommitted := false
	streamStatus := helper.StreamScannerHandler(c, resp, info, func(data string, sr *helper.StreamResult) {

		// 检查当前数据是否包含 completed 状态和 usage 信息
		var streamResponse dto.ResponsesStreamResponse
		if err := common.UnmarshalJsonStr(data, &streamResponse); err != nil {
			logger.LogError(c, "failed to unmarshal stream response: "+err.Error())
			sr.Error(err)
			return
		}
		if streamResponse.Type == "error" || streamResponse.Type == "response.error" || streamResponse.Type == "response.failed" || (streamResponse.Response != nil && streamResponse.Response.Error != nil) {
			sr.Stop(fmt.Errorf("upstream returned an error event"))
			return
		}
		if service.IsFreeModel(info.OriginModelName) && streamResponse.Response != nil {
			streamResponse.Response.Model = info.OriginModelName
			if encoded, marshalErr := common.Marshal(streamResponse); marshalErr == nil {
				data = string(encoded)
			} else {
				sr.Error(marshalErr)
				return
			}
		}
		if responsesStreamEventHasUsableOutput(streamResponse) {
			validOutput = true
		}
		if streamResponse.Response != nil && responsesResponseHasUsableOutput(streamResponse.Response) {
			validOutput = true
		}
		if streamResponse.Type == "response.incomplete" && responsesResponseHasValidEmptyTerminal(streamResponse.Response) {
			validOutput = true
		}
		if validOutput && !outputCommitted {
			c.Set(helper.ContextKeySuppressStreamPing, false)
			for _, pending := range pendingFrames {
				var pendingResponse dto.ResponsesStreamResponse
				if common.UnmarshalJsonStr(pending, &pendingResponse) == nil {
					sendResponsesStreamData(c, pendingResponse, pending)
				}
			}
			pendingFrames = nil
			outputCommitted = true
		}
		if !outputCommitted {
			pendingFrames = append(pendingFrames, data)
		} else {
			sendResponsesStreamData(c, streamResponse, data)
		}
		switch streamResponse.Type {
		case "response.completed":
			terminalFrame = true
			if streamResponse.Response != nil {
				if streamResponse.Response.Usage != nil {
					if streamResponse.Response.Usage.InputTokens != 0 {
						usage.PromptTokens = streamResponse.Response.Usage.InputTokens
					}
					if streamResponse.Response.Usage.OutputTokens != 0 {
						usage.CompletionTokens = streamResponse.Response.Usage.OutputTokens
					}
					if streamResponse.Response.Usage.TotalTokens != 0 {
						usage.TotalTokens = streamResponse.Response.Usage.TotalTokens
					}
					if streamResponse.Response.Usage.InputTokensDetails != nil {
						usage.PromptTokensDetails.CachedTokens = streamResponse.Response.Usage.InputTokensDetails.CachedTokens
					}
				}
				if streamResponse.Response.HasImageGenerationCall() {
					c.Set("image_generation_call", true)
					c.Set("image_generation_call_quality", streamResponse.Response.GetQuality())
					c.Set("image_generation_call_size", streamResponse.Response.GetSize())
				}
			}
		case "response.incomplete":
			// A partial response is usable if it already emitted output. An empty
			// one is accepted only for an explicit policy terminal such as
			// content_filter.
			terminalFrame = true
		case "response.output_text.delta":
			// 处理输出文本
			responseTextBuilder.WriteString(streamResponse.Delta)
		case dto.ResponsesOutputTypeItemDone:
			// 函数调用处理
			if streamResponse.Item != nil {
				switch streamResponse.Item.Type {
				case dto.BuildInCallWebSearchCall:
					if info != nil && info.ResponsesUsageInfo != nil && info.ResponsesUsageInfo.BuiltInTools != nil {
						if webSearchTool, exists := info.ResponsesUsageInfo.BuiltInTools[dto.BuildInToolWebSearchPreview]; exists && webSearchTool != nil {
							webSearchTool.CallCount++
						}
					}
				}
			}
		}
	})
	if streamErr := service.ValidateRelayStreamEnd(c, info, streamStatus, terminalFrame, validOutput); streamErr != nil {
		return nil, streamErr
	}

	if usage.CompletionTokens == 0 {
		// 计算输出文本的 token 数量
		tempStr := responseTextBuilder.String()
		if len(tempStr) > 0 {
			// 非正常结束，使用输出文本的 token 数量
			completionTokens := service.CountTextToken(tempStr, info.UpstreamModelName)
			usage.CompletionTokens = completionTokens
		}
	}

	if usage.PromptTokens == 0 && usage.CompletionTokens != 0 {
		usage.PromptTokens = info.GetEstimatePromptTokens()
	}

	usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens

	return usage, nil
}

func responsesStreamEventHasUsableOutput(event dto.ResponsesStreamResponse) bool {
	switch event.Type {
	case "response.output_text.delta", "response.reasoning_text.delta", "response.reasoning_summary_text.delta", "response.refusal.delta", "response.audio.delta", "response.function_call_arguments.delta":
		return strings.TrimSpace(event.Delta) != ""
	case "response.output_text.done", "response.reasoning_text.done", "response.reasoning_summary_text.done", "response.refusal.done", "response.audio.done":
		return strings.TrimSpace(event.Text) != "" || strings.TrimSpace(event.Refusal) != ""
	case "response.function_call_arguments.done":
		return event.Item != nil || len(event.Arguments) > 0
	case dto.ResponsesOutputTypeItemDone:
		return responsesOutputItemHasUsableOutput(event.Item)
	default:
		return false
	}
}

func responsesResponseHasValidEmptyTerminal(response *dto.OpenAIResponsesResponse) bool {
	if response == nil || response.IncompleteDetails == nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(response.IncompleteDetails.Reason)) {
	case "content_filter":
		return true
	default:
		return false
	}
}

func responsesResponseStatus(response *dto.OpenAIResponsesResponse) string {
	if response == nil {
		return ""
	}
	return strings.ToLower(strings.Trim(strings.TrimSpace(string(response.Status)), `"`))
}

func responsesResponseCanBeReturnedWithoutOutput(response *dto.OpenAIResponsesResponse) bool {
	if responsesResponseHasValidEmptyTerminal(response) {
		return true
	}
	if response == nil || !response.Background {
		return false
	}
	switch responsesResponseStatus(response) {
	case "queued", "in_progress":
		return true
	default:
		return false
	}
}

func responsesOutputItemHasUsableOutput(item *dto.ResponsesOutput) bool {
	if item == nil {
		return false
	}
	for _, content := range item.Content {
		if strings.TrimSpace(content.Text) != "" || strings.TrimSpace(content.Refusal) != "" {
			return true
		}
	}
	if strings.EqualFold(strings.TrimSpace(item.Status), "failed") {
		return false
	}
	switch item.Type {
	case "", "message":
		return false
	case "reasoning":
		if strings.TrimSpace(item.EncryptedContent) != "" {
			return true
		}
		for _, summary := range item.Summary {
			if strings.TrimSpace(summary.Text) != "" {
				return true
			}
		}
		return false
	case "function_call", "custom_tool_call":
		return strings.TrimSpace(item.Name) != "" &&
			(strings.TrimSpace(item.CallId) != "" || strings.TrimSpace(item.ID) != "" || len(item.Arguments) > 0)
	default:
		return strings.TrimSpace(item.ID) != "" || strings.EqualFold(strings.TrimSpace(item.Status), "completed")
	}
}

func responsesResponseHasUsableOutput(response *dto.OpenAIResponsesResponse) bool {
	if response == nil {
		return false
	}
	for i := range response.Output {
		if responsesOutputItemHasUsableOutput(&response.Output[i]) {
			return true
		}
	}
	return false
}
