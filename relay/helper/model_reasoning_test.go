package helper

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/types"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestGPT61ReasoningValidation(t *testing.T) {
	for _, model := range []string{"gpt-6.1-sol", "openai/gpt-6.1-sol"} {
		for _, effort := range []string{"", "low", "medium", "high", "xhigh", "max", "none", "minimal", "invalid"} {
			for _, responses := range []bool{false, true} {
				var request dto.Request
				if responses {
					request = &dto.OpenAIResponsesRequest{Model: model, Reasoning: &dto.Reasoning{Effort: effort}}
				} else {
					request = &dto.GeneralOpenAIRequest{Model: model, ReasoningEffort: effort}
				}
				before, marshalErr := common.Marshal(request)
				require.NoError(t, marshalErr)
				err := ValidateModelReasoning(request)
				after, marshalErr := common.Marshal(request)
				require.NoError(t, marshalErr)
				require.Equal(t, string(before), string(after), "must preserve the caller's effort")
				switch effort {
				case "none", "minimal", "invalid":
					require.NotNil(t, err)
					require.Equal(t, 400, err.StatusCode)
					require.True(t, types.IsSkipRetryError(err))
					public := err.ToOpenAIError()
					require.Equal(t, "invalid_request_error", public.Type)
					require.Equal(t, "unsupported_reasoning_effort", public.Code)
					require.Contains(t, public.Message, "medium (default)")
					if responses {
						require.Equal(t, "reasoning.effort", public.Param)
					} else {
						require.Equal(t, "reasoning_effort", public.Param)
					}
				default:
					require.Nil(t, err)
				}
			}
		}
	}
	require.Nil(t, ValidateModelReasoning(&dto.GeneralOpenAIRequest{Model: "gpt-6-sol", ReasoningEffort: "none"}))
	require.Nil(t, ValidateModelReasoning(&dto.OpenAIResponsesRequest{Model: "gpt-6.1-sol"}))
	var nested dto.GeneralOpenAIRequest
	require.NoError(t, common.UnmarshalJsonStr(`{"model":"gpt-6.1-sol","reasoning":{"effort":"none"}}`, &nested))
	err := ValidateModelReasoning(&nested)
	require.NotNil(t, err)
	require.Equal(t, "reasoning.effort", err.ToOpenAIError().Param)
}

func TestDeterministicParameterErrorIsNarrow(t *testing.T) {
	for _, tc := range []struct {
		status        int
		code, message string
		want          bool
	}{
		{400, "unknown_error", `gpt-6.1-sol does not support reasoning effort "none"`, true},
		{400, "unsupported_parameter", "invalid argument", true},
		{400, "unknown_error", "channel does not support this model", false},
		{502, "unknown_error", "Bad Gateway", false},
		{524, "unknown_error", "Timeout", false},
		{429, "rate_limit_exceeded", "Too many requests", false},
	} {
		err := types.WithOpenAIError(types.OpenAIError{Code: tc.code, Message: tc.message}, tc.status)
		require.Equal(t, tc.want, IsDeterministicParameterError(err), tc.message)
	}
}
