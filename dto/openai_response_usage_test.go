package dto

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestInputTokenDetailsAcceptsCacheCreationAliases(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want int
	}{
		{
			name: "canonical cached creation tokens",
			raw:  `{"cached_tokens":7,"cached_creation_tokens":41,"text_tokens":53}`,
			want: 41,
		},
		{
			name: "anthropic compatible cache creation input tokens",
			raw:  `{"cached_tokens":7,"cache_creation_input_tokens":42,"text_tokens":54}`,
			want: 42,
		},
		{
			name: "canonical value wins when both exist",
			raw:  `{"cached_creation_tokens":43,"cache_creation_input_tokens":44}`,
			want: 43,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var details InputTokenDetails
			require.NoError(t, details.UnmarshalJSON([]byte(tt.raw)))
			require.Equal(t, tt.want, details.CachedCreationTokens)
		})
	}
}

func TestUsageNormalizesCacheCreationInputTokens(t *testing.T) {
	var usage Usage
	require.NoError(t, json.Unmarshal([]byte(`{
		"prompt_tokens":5055,
		"completion_tokens":38,
		"total_tokens":5093,
		"prompt_tokens_details":{
			"cached_tokens":0,
			"cache_creation_input_tokens":5042,
			"text_tokens":5055
		}
	}`), &usage))
	require.Equal(t, 5055, usage.PromptTokens)
	require.Equal(t, 38, usage.CompletionTokens)
	require.Equal(t, 5042, usage.PromptTokensDetails.CachedCreationTokens)
}
