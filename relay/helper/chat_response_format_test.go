package helper_test

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relay/channel/openai"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

const completionJSON = `{"id":"completion-format-test","object":"chat.completion","model":"grok-4.6","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":1,"total_tokens":4}}`

type trackedResponseBody struct {
	io.Reader
	closed bool
}

func (b *trackedResponseBody) Close() error { b.closed = true; return nil }

func TestMislabeledChatJSONUsesJSONDecoderAndPreservesUsage(t *testing.T) {
	for _, prefix := range []string{"", " \r\n\t"} {
		t.Run(prefix, func(t *testing.T) {
			body := &trackedResponseBody{Reader: strings.NewReader(prefix + completionJSON)}
			resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream; charset=utf-8"}}, Body: body}
			stream, err := helper.NormalizeChatResponseFormat(resp, false)
			require.NoError(t, err)
			require.False(t, stream)
			require.False(t, body.closed)

			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			info := &relaycommon.RelayInfo{IsStream: stream, RelayFormat: types.RelayFormatOpenAI, ChannelMeta: &relaycommon.ChannelMeta{}}
			usage, relayErr := (&openai.Adaptor{}).DoResponse(c, resp, info)
			require.Nil(t, relayErr)
			require.Equal(t, 200, recorder.Code)
			require.Equal(t, "application/json", recorder.Header().Get("Content-Type"))
			require.JSONEq(t, completionJSON, recorder.Body.String())
			require.NotNil(t, usage)
			require.Contains(t, recorder.Body.String(), `"prompt_tokens":3`)
			require.True(t, body.closed)
		})
	}
}

func TestChatResponseFormatDoesNotCarryStreamStateToNextResponse(t *testing.T) {
	info := &relaycommon.RelayInfo{}
	for _, test := range []struct {
		contentType, body string
		wantStream        bool
	}{
		{"text/event-stream", "data: [DONE]\n\n", true},
		{"application/json", completionJSON, false},
		{"text/event-stream", completionJSON, false},
	} {
		resp := &http.Response{Header: http.Header{"Content-Type": {test.contentType}}, Body: io.NopCloser(strings.NewReader(test.body))}
		var err error
		info.IsStream, err = helper.NormalizeChatResponseFormat(resp, false)
		require.NoError(t, err)
		require.Equal(t, test.wantStream, info.IsStream)
		all, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.Equal(t, test.body, string(all))
		require.NoError(t, resp.Body.Close())
	}
}

func TestChatResponseFormatKeepsRealStreamsStreaming(t *testing.T) {
	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 5
	t.Cleanup(func() { constant.StreamingTimeout = oldTimeout })
	for _, requestedStream := range []bool{false, true} {
		resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(
			"data: {\"id\":\"stream\",\"model\":\"grok-4.6\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"))}
		stream, err := helper.NormalizeChatResponseFormat(resp, requestedStream)
		require.NoError(t, err)
		require.True(t, stream)
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
		info := &relaycommon.RelayInfo{IsStream: stream, StartTime: time.Now(), OriginModelName: "grok-4.6", RelayFormat: types.RelayFormatOpenAI, ChannelMeta: &relaycommon.ChannelMeta{}}
		_, relayErr := (&openai.Adaptor{}).DoResponse(c, resp, info)
		require.Nil(t, relayErr)
		require.Contains(t, recorder.Body.String(), "data: [DONE]")
		require.Contains(t, recorder.Body.String(), `"content":"ok"`)
	}
}

func TestChatResponseFormatDoesNotWaitForStreamEOF(t *testing.T) {
	reader, writer := io.Pipe()
	t.Cleanup(func() { _ = reader.Close(); _ = writer.Close() })
	go func() { _, _ = writer.Write([]byte("data: ")) }()
	resp := &http.Response{Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: reader}
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		_, _ = helper.NormalizeChatResponseFormat(resp, false)
	}()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("format detection waited for the complete upstream stream")
	}
}

type failedFormatReader struct{}

func (failedFormatReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestChatResponseFormatPropagatesTransportErrorsAndClose(t *testing.T) {
	body := &trackedResponseBody{Reader: failedFormatReader{}}
	resp := &http.Response{Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: body}
	_, err := helper.NormalizeChatResponseFormat(resp, false)
	require.True(t, errors.Is(err, io.ErrUnexpectedEOF))
	require.NoError(t, resp.Body.Close())
	require.True(t, body.closed)
}

func TestMislabeledInvalidChatJSONStillFails(t *testing.T) {
	resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(`{"choices":`))}
	stream, err := helper.NormalizeChatResponseFormat(resp, false)
	require.NoError(t, err)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	info := &relaycommon.RelayInfo{IsStream: stream, RelayFormat: types.RelayFormatOpenAI, ChannelMeta: &relaycommon.ChannelMeta{}}
	_, relayErr := (&openai.Adaptor{}).DoResponse(c, resp, info)
	require.NotNil(t, relayErr)
	require.False(t, c.Writer.Written())
}
