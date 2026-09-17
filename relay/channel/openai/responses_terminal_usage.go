package openai

import "github.com/QuantumNous/new-api/dto"

func responsesTerminalUsage(event dto.ResponsesStreamResponse) (*dto.Usage, bool) {
	if (event.Type != "response.completed" && event.Type != "response.incomplete") || event.Response == nil || event.Response.Usage == nil {
		return nil, false
	}
	status := responsesResponseStatus(event.Response)
	if status != "" && "response."+status != event.Type {
		return nil, false
	}
	u := *event.Response.Usage
	if u.InputTokens < 0 || u.OutputTokens < 0 || u.TotalTokens < 0 {
		return nil, false
	}
	total := u.InputTokens + u.OutputTokens
	if total < 0 || (u.TotalTokens != 0 && u.TotalTokens != total) || (total == 0 && !responsesResponseHasValidEmptyTerminal(event.Response)) {
		return nil, false
	}
	u.PromptTokens, u.CompletionTokens, u.TotalTokens = u.InputTokens, u.OutputTokens, total
	if u.InputTokensDetails != nil {
		if u.InputTokensDetails.CachedTokens < 0 || u.InputTokensDetails.CachedTokens > u.InputTokens {
			return nil, false
		}
		u.PromptTokensDetails.CachedTokens = u.InputTokensDetails.CachedTokens
		u.PromptTokensDetails.ImageTokens = u.InputTokensDetails.ImageTokens
		u.PromptTokensDetails.AudioTokens = u.InputTokensDetails.AudioTokens
	}
	return &u, true
}
