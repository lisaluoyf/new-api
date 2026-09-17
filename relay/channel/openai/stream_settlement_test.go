package openai

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// The upstream keeps the connection open after its terminal event, as Responses
// does not require a trailing [DONE]. Closing the body must release the reader.
type terminalStreamBody struct {
	*strings.Reader
	closed chan struct{}
	once   sync.Once
}

func (b *terminalStreamBody) Read(p []byte) (int, error) {
	n, err := b.Reader.Read(p)
	if err == io.EOF {
		<-b.closed
		return 0, context.Canceled
	}
	return n, err
}

func (b *terminalStreamBody) Close() error {
	b.once.Do(func() { close(b.closed) })
	return nil
}

type cancelOnFrameWriter struct {
	gin.ResponseWriter
	match  string
	cancel context.CancelFunc
}

func (w *cancelOnFrameWriter) Write(p []byte) (int, error) {
	n, err := w.ResponseWriter.Write(p)
	if strings.Contains(string(p), w.match) {
		w.cancel()
		// Let the scanner observe cancellation before the callback returns.
		time.Sleep(20 * time.Millisecond)
	}
	return n, err
}

func TestResponsesTerminalCancellationSettlement(t *testing.T) {
	for _, format := range []types.RelayFormat{types.RelayFormatOpenAIResponses, types.RelayFormatOpenAI, types.RelayFormatClaude} {
		for _, terminal := range []string{"response.completed", "response.incomplete"} {
			name := terminal + "/" + string(format)
			t.Run(name, func(t *testing.T) {
				c, _ := responsesStreamTestContext(t)
				ctx, cancel := context.WithCancel(c.Request.Context())
				defer cancel()
				c.Request = c.Request.WithContext(ctx)
				info := responsesStreamTestInfo()
				info.RelayFormat = format
				handler := OaiResponsesStreamHandler
				match := terminal
				if format != types.RelayFormatOpenAIResponses {
					info.ShouldIncludeUsage = true
					handler = OaiResponsesToChatStreamHandler
					match = `"finish_reason":"stop"`
					if format == types.RelayFormatClaude {
						match = "message_stop"
					}
				}
				c.Writer = &cancelOnFrameWriter{ResponseWriter: c.Writer, match: match, cancel: cancel}
				body := &terminalStreamBody{Reader: strings.NewReader(
					"data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n" +
						"data: {\"type\":\"" + terminal + "\",\"response\":{\"id\":\"resp_settlement\",\"usage\":{\"input_tokens\":120,\"output_tokens\":7,\"total_tokens\":127,\"input_tokens_details\":{\"cached_tokens\":100}}}}\n\n"), closed: make(chan struct{})}
				timer := time.AfterFunc(2*time.Second, func() { cancel(); _ = body.Close() })
				defer timer.Stop()
				started := time.Now()
				usage, err := handler(c, info, &http.Response{StatusCode: 200, Body: body})
				require.Nil(t, err)
				require.Less(t, time.Since(started), time.Second)
				require.Equal(t, 120, usage.PromptTokens)
				require.Equal(t, 7, usage.CompletionTokens)
				require.Equal(t, 100, usage.PromptTokensDetails.CachedTokens)
			})
		}
	}
}

func TestResponsesTerminalWithoutUsageCannotSettleCancellation(t *testing.T) {
	c, _ := responsesStreamTestContext(t)
	ctx, cancel := context.WithCancel(c.Request.Context())
	defer cancel()
	c.Request = c.Request.WithContext(ctx)
	c.Writer = &cancelOnFrameWriter{ResponseWriter: c.Writer, match: "response.completed", cancel: cancel}
	body := &terminalStreamBody{Reader: strings.NewReader(
		"data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n" +
			"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_no_usage\"}}\n\n"), closed: make(chan struct{})}
	usage, err := OaiResponsesStreamHandler(c, responsesStreamTestInfo(), &http.Response{StatusCode: 200, Body: body})
	require.NotNil(t, err)
	require.Nil(t, usage)
}

func TestResponsesEarlyCancellationDoesNotEstimateSettlement(t *testing.T) {
	for _, handler := range []func(*gin.Context, *relaycommon.RelayInfo, *http.Response) (*dto.Usage, *types.NewAPIError){OaiResponsesStreamHandler, OaiResponsesToChatStreamHandler} {
		c, _ := responsesStreamTestContext(t)
		ctx, cancel := context.WithCancel(c.Request.Context())
		defer cancel()
		c.Request = c.Request.WithContext(ctx)
		c.Writer = &cancelOnFrameWriter{ResponseWriter: c.Writer, match: "hello", cancel: cancel}
		info := responsesStreamTestInfo()
		info.RelayFormat = types.RelayFormatOpenAI
		body := &terminalStreamBody{Reader: strings.NewReader("data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n"), closed: make(chan struct{})}
		usage, err := handler(c, info, &http.Response{StatusCode: 200, Body: body})
		require.NotNil(t, err)
		require.Nil(t, usage)
	}
}
