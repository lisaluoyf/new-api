package openai

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestFreeModelNonStreamResponseUsesVirtualModelID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	info := &relaycommon.RelayInfo{OriginModelName: service.FreeModelID, RelayFormat: types.RelayFormatOpenAI, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "provider/real-free"}}
	info.SetEstimatePromptTokens(1)
	resp := &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"id":"x","model":"provider/real-free","object":"chat.completion","choices":[],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))}
	_, apiErr := OpenaiHandler(ctx, info, resp)
	require.Nil(t, apiErr)
	var body map[string]interface{}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &body))
	require.Equal(t, service.FreeModelID, body["model"])
}

func TestFreeModelStreamChunkUsesVirtualModelID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	info := &relaycommon.RelayInfo{OriginModelName: service.FreeModelID}
	err := sendStreamData(ctx, info, `{"id":"x","object":"chat.completion.chunk","created":1,"model":"provider/real-free","choices":[]}`, false, false)
	require.NoError(t, err)
	require.Contains(t, recorder.Body.String(), `"model":"apimaster-freemodel"`)
}
