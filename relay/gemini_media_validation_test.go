package relay

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"net/http/httptest"
	"testing"
)

func TestGeminiBadImageRejectedBeforeUpstream(t *testing.T) {
	for _, native := range []bool{false, true} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
		info := &relaycommon.RelayInfo{OriginModelName: "gemini-4-argon"}
		var e *types.NewAPIError
		if native {
			var req dto.GeminiChatRequest
			require.NoError(t, common.Unmarshal([]byte(`{"contents":[{"parts":[{"inline_data":{"mime_type":"image/png","data":"bad!"}}]}]}`), &req))
			info.Request = &req
			e = GeminiHelper(c, info)
		} else {
			var req dto.GeneralOpenAIRequest
			require.NoError(t, common.Unmarshal([]byte(`{"model":"gemini-4-argon","messages":[{"role":"user","content":"![x](data:image/png;base64,bad!)"}]}`), &req))
			info.Request = &req
			e = TextHelper(c, info)
		}
		require.NotNil(t, e)
		require.Equal(t, 400, e.StatusCode)
		require.True(t, types.IsSkipRetryError(e))
		require.Equal(t, types.ErrorCodeInvalidRequest, e.GetErrorCode())
		require.Equal(t, service.CategorySkip, service.ClassifyChannelError(e))
	}
}
