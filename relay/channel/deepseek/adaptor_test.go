package deepseek

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/stretchr/testify/require"
)

func TestConvertOpenAIRequestSupportsV4VisionThinkingSuffix(t *testing.T) {
	request := &dto.GeneralOpenAIRequest{Model: "deepseek-v4-flash-vision-exp-max"}

	converted, err := (&Adaptor{}).ConvertOpenAIRequest(nil, nil, request)
	require.NoError(t, err)
	require.Same(t, request, converted)
	require.Equal(t, "deepseek-v4-flash-vision-exp", request.Model)
	require.Equal(t, "max", request.ReasoningEffort)

	var thinking map[string]string
	require.NoError(t, common.Unmarshal(request.THINKING, &thinking))
	require.Equal(t, "enabled", thinking["type"])
}
