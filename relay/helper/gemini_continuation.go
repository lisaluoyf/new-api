package helper

import (
	"github.com/QuantumNous/new-api/dto"
	"strings"
)

// Gemini 4 no longer accepts assistant/model prefill. Preserve the entire
// history and request a continuation, without fabricating missing tool results.
func NormalizeGemini4Continuation(model string, request *dto.GeneralOpenAIRequest) {
	if !strings.HasPrefix(strings.TrimPrefix(model, "google/"), "gemini-4-") || request == nil || len(request.Messages) == 0 {
		return
	}
	last := request.Messages[len(request.Messages)-1]
	if last.Role != "assistant" || len(last.ToolCalls) > 0 {
		return
	}
	messages := make([]dto.Message, len(request.Messages), len(request.Messages)+1)
	copy(messages, request.Messages)
	request.Messages = append(messages, dto.Message{Role: "user", Content: "Continue from the end of your previous response without repeating it."})
}
