package openai

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
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
	ctx.Set("original_model", service.FreeModelID)
	info := &relaycommon.RelayInfo{OriginModelName: service.FreeModelID, RelayFormat: types.RelayFormatOpenAI, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "provider/real-free"}}
	info.SetEstimatePromptTokens(1)
	resp := &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"id":"x","model":"provider/real-free","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))}
	_, apiErr := OpenaiHandler(ctx, info, resp)
	require.Nil(t, apiErr)
	var body map[string]interface{}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &body))
	require.Equal(t, service.FreeModelID, body["model"])
}

func TestFreeModelHTTP200ErrorObjectIsNotAccepted(t *testing.T) {
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Set("original_model", service.FreeModelID)
	info := &relaycommon.RelayInfo{OriginModelName: service.FreeModelID, RelayFormat: types.RelayFormatOpenAI, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "provider/real-free"}}
	resp := &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"error":{"message":"temporarily unavailable","code":"temporarily_unavailable"}}`))}
	_, apiErr := OpenaiHandler(ctx, info, resp)
	require.NotNil(t, apiErr)
	require.Zero(t, recorder.Body.Len())
}

type failingStreamBody struct {
	data []byte
	sent bool
}

func (r *failingStreamBody) Read(p []byte) (int, error) {
	if r.sent {
		return 0, io.ErrUnexpectedEOF
	}
	r.sent = true
	return copy(p, r.data), nil
}
func (r *failingStreamBody) Close() error { return nil }

func newFreeModelStreamContext() (*gin.Context, *httptest.ResponseRecorder, *relaycommon.RelayInfo) {
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx.Set("original_model", service.FreeModelID)
	info := &relaycommon.RelayInfo{OriginModelName: service.FreeModelID, RelayFormat: types.RelayFormatOpenAI, IsStream: true, StartTime: time.Now(), ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "provider/free"}}
	info.SetEstimatePromptTokens(1)
	return ctx, recorder, info
}

func ensureStreamTimeout(t *testing.T) {
	old := constant.StreamingTimeout
	if old <= 0 {
		constant.StreamingTimeout = 5
	}
	t.Cleanup(func() { constant.StreamingTimeout = old })
}

func TestFreeModelStreamFailureBeforeFirstFrameCanFallback(t *testing.T) {
	ensureStreamTimeout(t)
	ctx, recorder, info := newFreeModelStreamContext()
	resp := &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: &failingStreamBody{sent: true}}
	_, apiErr := OaiStreamHandler(ctx, info, resp)
	require.NotNil(t, apiErr)
	require.False(t, recorder.Result().Body != nil && recorder.Body.Len() > 0)
	require.False(t, ctx.Writer.Written())
}

func TestFreeModelStreamFailureAfterFirstFrameDoesNotPermitTransparentFallback(t *testing.T) {
	ensureStreamTimeout(t)
	ctx, recorder, info := newFreeModelStreamContext()
	data := "data: {\"id\":\"x\",\"model\":\"provider/free\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"a\"},\"finish_reason\":null}]}\n\n" +
		"data: {\"id\":\"x\",\"model\":\"provider/free\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"b\"},\"finish_reason\":null}]}\n\n"
	resp := &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: &failingStreamBody{data: []byte(data)}}
	_, apiErr := OaiStreamHandler(ctx, info, resp)
	require.NotNil(t, apiErr)
	require.True(t, ctx.Writer.Written())
	require.Contains(t, recorder.Body.String(), service.FreeModelID)
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
