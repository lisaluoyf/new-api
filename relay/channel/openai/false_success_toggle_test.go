package openai

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func withFalseSuccessFallback(t *testing.T, enabled bool) {
	t.Helper()
	original := operation_setting.IsUpstreamFalseSuccessEnabled()
	operation_setting.SetUpstreamFalseSuccessEnabled(enabled)
	t.Cleanup(func() { operation_setting.SetUpstreamFalseSuccessEnabled(original) })
}

func falseSuccessToggleStreamContext(t *testing.T) (*gin.Context, *httptest.ResponseRecorder, *common.RelayInfo) {
	t.Helper()
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	info := &common.RelayInfo{
		OriginModelName: "gpt-5.6-terra",
		RelayFormat:     types.RelayFormatOpenAI,
		IsStream:        true,
		StartTime:       time.Now(),
		ChannelMeta:     &common.ChannelMeta{UpstreamModelName: "gpt-5.6-terra"},
	}
	return ctx, recorder, info
}

func TestFalseSuccessToggleOffKeepsLifecycleOnlyResponsesStream(t *testing.T) {
	withFalseSuccessFallback(t, false)
	c, recorder := responsesStreamTestContext(t)
	body := "data: {\"type\":\"response.created\",\"response\":{\"status\":\"in_progress\"}}\n\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[]}}\n\n"

	_, err := OaiResponsesStreamHandler(c, responsesStreamTestInfo(), responsesHTTPStream(body))
	require.Nil(t, err)
	require.False(t, c.Writer.Written())
	require.Empty(t, recorder.Body.String())
}

func TestFalseSuccessToggleOffKeepsLifecycleOnlyResponsesToChatStream(t *testing.T) {
	withFalseSuccessFallback(t, false)
	c, recorder := responsesStreamTestContext(t)
	info := responsesStreamTestInfo()
	info.RelayFormat = types.RelayFormatOpenAI
	body := "data: {\"type\":\"response.created\",\"response\":{\"status\":\"in_progress\"}}\n\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[]}}\n\n"

	_, err := OaiResponsesToChatStreamHandler(c, info, responsesHTTPStream(body))
	require.Nil(t, err)
	// 关掉假成功判定后，空流按普通结束处理，只补发结束帧而不是换渠道重试。
	require.True(t, c.Writer.Written())
	require.Contains(t, recorder.Body.String(), "data: [DONE]")
}

func TestFalseSuccessToggleOffKeepsEmptyChatStream(t *testing.T) {
	ensureStreamTimeout(t)
	withFalseSuccessFallback(t, false)
	ctx, recorder, info := falseSuccessToggleStreamContext(t)
	body := "data: {\"id\":\"x\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"

	_, err := OaiStreamHandler(ctx, info, responsesHTTPStream(body))
	require.Nil(t, err)
	// 关掉假成功判定后不再扣帧：空流按普通结束处理，帧原样透传，不会出现
	// 「客户端拿到空流却照常计费」。
	require.True(t, ctx.Writer.Written())
	require.Contains(t, recorder.Body.String(), `"finish_reason":"stop"`)
	require.Contains(t, recorder.Body.String(), "data: [DONE]")
}

func TestOpenAIStreamErrorEventNarrowsWhenDisabled(t *testing.T) {
	wrapped := `{"type":"response.failed","response":{"error":{"type":"server_error","code":"server_is_overloaded","message":"overloaded"}}}`
	topLevel := `{"error":{"type":"server_error","code":"server_is_overloaded","message":"overloaded"}}`

	withFalseSuccessFallback(t, false)
	_, _, ok := openAIStreamErrorEvent(wrapped)
	require.False(t, ok)
	_, _, ok = openAIStreamErrorEvent(topLevel)
	require.True(t, ok)

	withFalseSuccessFallback(t, true)
	upstreamErr, eventType, ok := openAIStreamErrorEvent(wrapped)
	require.True(t, ok)
	require.Equal(t, "response.failed", eventType)
	require.Equal(t, "server_is_overloaded", fmt.Sprint(upstreamErr.Code))
}

func TestFalseSuccessToggleOffIgnoresWrappedChatStreamError(t *testing.T) {
	ensureStreamTimeout(t)
	body := "data: {\"type\":\"response.failed\",\"response\":{\"error\":{\"type\":\"server_error\",\"code\":\"server_is_overloaded\",\"message\":\"overloaded\"}}}\n\n" +
		"data: {\"id\":\"x\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"},\"finish_reason\":null}]}\n\n" +
		"data: {\"id\":\"x\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"

	withFalseSuccessFallback(t, false)
	ctx, recorder, info := falseSuccessToggleStreamContext(t)
	_, err := OaiStreamHandler(ctx, info, responsesHTTPStream(body))
	require.Nil(t, err)
	require.Contains(t, recorder.Body.String(), "response.failed")

	withFalseSuccessFallback(t, true)
	ctx, _, info = falseSuccessToggleStreamContext(t)
	_, err = OaiStreamHandler(ctx, info, responsesHTTPStream(body))
	require.NotNil(t, err)
	require.Equal(t, http.StatusBadGateway, err.StatusCode)
	require.NotNil(t, err.UpstreamFalseSuccess)
}

func TestFalseSuccessToggleOffKeepsUpstreamStatusForErrorBody(t *testing.T) {
	withFalseSuccessFallback(t, false)
	c, _ := responsesStreamTestContext(t)
	body := `{"status":"failed","error":{"type":"server_error","code":"server_is_overloaded","message":"Selected model is at capacity"}}`

	_, err := OaiResponsesHandler(c, responsesStreamTestInfo(), responsesHTTPStream(body))
	require.NotNil(t, err)
	require.Nil(t, err.UpstreamFalseSuccess)
	require.Equal(t, http.StatusOK, err.StatusCode)

	c2, _ := responsesStreamTestContext(t)
	info := responsesStreamTestInfo()
	info.RelayFormat = types.RelayFormatOpenAI
	_, err2 := OaiResponsesToChatHandler(c2, info, responsesHTTPStream(body))
	require.NotNil(t, err2)
	require.Nil(t, err2.UpstreamFalseSuccess)
	require.Equal(t, http.StatusOK, err2.StatusCode)
}
