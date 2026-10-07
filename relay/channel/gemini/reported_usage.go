package gemini

import (
	"strconv"

	"github.com/QuantumNous/new-api/dto"
	"github.com/tidwall/gjson"
)

func reportedGeminiUsage(data string, metadata dto.GeminiUsageMetadata) *dto.Usage {
	root := gjson.Get(data, "usageMetadata")
	for _, field := range []string{"promptTokenCount", "totalTokenCount", "candidatesTokenCount", "thoughtsTokenCount", "cachedContentTokenCount"} {
		v := root.Get(field)
		if !v.Exists() && field != "promptTokenCount" && field != "totalTokenCount" {
			continue
		}
		n, err := strconv.ParseInt(v.Raw, 10, 32)
		if v.Type != gjson.Number || err != nil || n < 0 {
			return nil
		}
	}
	valid := true
	root.ForEach(func(key, value gjson.Result) bool {
		if value.Type == gjson.Number {
			n, err := strconv.ParseInt(value.Raw, 10, 32)
			if err != nil || n < 0 {
				valid = false
			}
		}
		return valid
	})
	if !valid {
		return nil
	}
	usage := buildUsageFromGeminiMetadata(metadata, 0)
	if usage.PromptTokens < 0 || usage.CompletionTokens < 0 || usage.TotalTokens < usage.PromptTokens || usage.PromptTokensDetails.CachedTokens > usage.PromptTokens {
		return nil
	}
	usage.UsageSource = "upstream_reported"
	return &usage
}
