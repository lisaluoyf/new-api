package openai

import (
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/tidwall/gjson"
)

func reportedChatUsage(data string) (*dto.Usage, string, bool) {
	root := gjson.Get(data, "usage")
	for _, field := range []string{"prompt_tokens", "completion_tokens"} {
		v := root.Get(field)
		n, err := strconv.ParseInt(v.Raw, 10, 32)
		if v.Type != gjson.Number || err != nil || n < 0 {
			return nil, "", false
		}
	}
	var usage dto.Usage
	if common.UnmarshalJsonStr(root.Raw, &usage) != nil {
		return nil, "", false
	}
	// Invalid cache details must not become a negative charge.
	for _, field := range []string{"prompt_tokens_details.cached_tokens", "prompt_cache_hit_tokens", "prompt_cache_miss_tokens", "input_tokens_details.cached_tokens"} {
		v := root.Get(field)
		if !v.Exists() {
			continue
		}
		n, err := strconv.ParseInt(v.Raw, 10, 32)
		if v.Type != gjson.Number || err != nil || n < 0 || int(n) > usage.PromptTokens {
			return nil, "", false
		}
	}
	usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	usage.UsageSource = "upstream_reported"
	id := gjson.Get(data, "id").String()
	terminal := len(gjson.Get(data, "choices").Array()) == 0
	for _, choice := range gjson.Get(data, "choices").Array() {
		if choice.Get("finish_reason").String() != "" {
			terminal = true
		}
	}
	return &usage, id, terminal
}
