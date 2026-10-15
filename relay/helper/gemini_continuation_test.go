package helper

import (
	"github.com/QuantumNous/new-api/dto"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestNormalizeGemini4Continuation(t *testing.T) {
	for _, model := range []string{"gemini-4-argon", "google/gemini-4-argon", "gemini-3-flash"} {
		r := dto.GeneralOpenAIRequest{Messages: []dto.Message{{Role: "user", Content: "Hello"}, {Role: "assistant", Content: "Start"}}}
		NormalizeGemini4Continuation(model, &r)
		if model == "gemini-3-flash" {
			require.Len(t, r.Messages, 2)
		} else {
			require.Len(t, r.Messages, 3)
			require.Equal(t, "Start", r.Messages[1].Content)
			NormalizeGemini4Continuation(model, &r)
			require.Len(t, r.Messages, 3)
		}
	}
	r := dto.GeneralOpenAIRequest{Messages: []dto.Message{{Role: "assistant", ToolCalls: []byte(`[{"id":"call_1"}]`)}}}
	NormalizeGemini4Continuation("gemini-4-argon", &r)
	require.Len(t, r.Messages, 1)
	for _, empty := range []string{"null", "[]"} {
		r := dto.GeneralOpenAIRequest{Messages: []dto.Message{{Role: "assistant", Content: "Start", ToolCalls: []byte(empty)}}}
		NormalizeGemini4Continuation("gemini-4-argon", &r)
		require.Len(t, r.Messages, 2)
	}

}
