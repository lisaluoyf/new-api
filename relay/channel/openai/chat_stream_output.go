package openai

import (
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/relay/helper"
)

const (
	maxChatPreOutputFrames = 128
	maxChatPreOutputBytes  = 64 * 1024
)

type chatStreamFrameOutput struct {
	kind           string
	usable         bool
	contentChars   int
	reasoningChars int
	toolValueChars int
	refusalChars   int
}

// Scanner cleanup can return while a blocked downstream writer is still
// exiting. Take immutable, synchronized snapshots for the retry log.
type chatStreamOutputDiagnostics struct {
	mu   sync.Mutex
	data helper.StreamOutputDiagnostics
}

func (d *chatStreamOutputDiagnostics) observe(frame chatStreamFrameOutput, errorEvent bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.data.Frames++
	d.data.ContentChars += frame.contentChars
	d.data.ReasoningChars += frame.reasoningChars
	d.data.ToolValueChars += frame.toolValueChars
	d.data.RefusalChars += frame.refusalChars
	kind := frame.kind
	if errorEvent {
		kind = "error"
	}
	d.data.LastFrameKind = kind
	if d.data.FirstFrameKind == "" {
		d.data.FirstFrameKind = kind
	}
	if !errorEvent && frame.usable && d.data.FirstUsableKind == "" {
		d.data.FirstUsableKind = kind
	}
}

func (d *chatStreamOutputDiagnostics) buffer(bytes int, limitReached bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if limitReached {
		d.data.BufferLimitReached = true
		return
	}
	d.data.BufferedFrames++
	d.data.BufferedBytes += bytes
}

func (d *chatStreamOutputDiagnostics) snapshot() helper.StreamOutputDiagnostics {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.data
}

func inspectChatStreamOutput(data string) chatStreamFrameOutput {
	var response struct {
		Choices []struct {
			Delta struct {
				dto.ChatCompletionsStreamResponseChoiceDelta
				FunctionCall *dto.FunctionResponse `json:"function_call"`
				Refusal      string                `json:"refusal"`
			} `json:"delta"`
			FinishReason *string `json:"finish_reason"`
		} `json:"choices"`
	}
	result := chatStreamFrameOutput{kind: "metadata"}
	if common.UnmarshalJsonStr(data, &response) != nil {
		result.kind = "invalid_frame"
		return result
	}
	markUsable := func(kind string) {
		if !result.usable {
			result.kind = kind
		}
		result.usable = true
	}
	for _, choice := range response.Choices {
		content := choice.Delta.GetContentString()
		reasoning := choice.Delta.GetReasoningContent()
		result.contentChars += utf8.RuneCountInString(content)
		result.reasoningChars += utf8.RuneCountInString(reasoning)
		if !result.usable {
			switch {
			case content != "" || reasoning != "":
				result.kind = "blank_text"
			case len(choice.Delta.ToolCalls) > 0 || choice.Delta.FunctionCall != nil:
				result.kind = "tool_shell"
			case choice.Delta.Role != "":
				result.kind = "role"
			default:
				result.kind = "empty_delta"
			}
		}
		if strings.TrimSpace(content) != "" {
			markUsable("content")
		}
		if strings.TrimSpace(reasoning) != "" {
			markUsable("reasoning")
		}
		for _, tool := range choice.Delta.ToolCalls {
			result.toolValueChars += utf8.RuneCountInString(tool.Function.Name) + utf8.RuneCountInString(tool.Function.Arguments)
			// Preserve the conservative handling of future tool types whose
			// payload is not represented by the function-call DTO.
			if kind, ok := tool.Type.(string); ok && kind != "" && kind != "function" {
				markUsable("tool_call")
			}
			// An index, ID or type alone is only the tool-call envelope. Keep it
			// buffered until a function name or argument fragment actually arrives.
			if strings.TrimSpace(tool.Function.Name) != "" || strings.TrimSpace(tool.Function.Arguments) != "" {
				markUsable("tool_call")
			}
		}
		if function := choice.Delta.FunctionCall; function != nil {
			result.toolValueChars += utf8.RuneCountInString(function.Name) + utf8.RuneCountInString(function.Arguments)
			if strings.TrimSpace(function.Name) != "" || strings.TrimSpace(function.Arguments) != "" {
				markUsable("function_call")
			}
		}
		result.refusalChars += utf8.RuneCountInString(choice.Delta.Refusal)
		if strings.TrimSpace(choice.Delta.Refusal) != "" {
			markUsable("refusal")
		}
		if choice.FinishReason != nil && *choice.FinishReason == constant.FinishReasonContentFilter {
			markUsable("content_filter")
		}
	}
	return result
}

func chatStreamDataHasUsableOutput(data string) bool {
	return inspectChatStreamOutput(data).usable
}
