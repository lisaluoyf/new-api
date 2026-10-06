package claude

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type cancelOnClaudeFrameWriter struct {
	gin.ResponseWriter
	buffer *httptest.ResponseRecorder
	frame  string
	cancel context.CancelFunc
}

func (w *cancelOnClaudeFrameWriter) Flush() {
	w.ResponseWriter.Flush()
	if strings.Contains(w.buffer.Body.String(), w.frame) {
		w.cancel()
	}
}

func TestClaudeCanceledStreamUsesReportedCountsWithoutEstimating(t *testing.T) {
	old := constant.StreamingTimeout
	constant.StreamingTimeout = 5
	t.Cleanup(func() { constant.StreamingTimeout = old })
	for _, tc := range []struct {
		name, start, end, cancelFrame string
		user                          int
		wantCharge                    bool
		wantOutput                    int
	}{
		{"early", `{"input_tokens":100,"output_tokens":1,"cache_read_input_tokens":200,"cache_creation_input_tokens":30,"cache_creation":{"ephemeral_5m_input_tokens":10,"ephemeral_1h_input_tokens":20}}`, "", "text_delta", 123, true, 1},
		{"missing_input", `{"output_tokens":1}`, "", "text_delta", 123, false, 0},
		{"invalid_count", `{"input_tokens":-1,"output_tokens":1}`, "", "text_delta", 123, false, 0},
		{"internal_loser", `{"input_tokens":100,"output_tokens":1}`, "", "text_delta", 0, false, 0},
		{"terminal", `{"input_tokens":100,"output_tokens":1}`, `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":40}}`, "message_delta", 123, true, 40},
		{"explicit_zero", `{"input_tokens":0,"output_tokens":0}`, `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":0}}`, "message_delta", 123, true, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			c.Request = httptest.NewRequest("POST", "/v1/messages", nil).WithContext(ctx)
			c.Writer = &cancelOnClaudeFrameWriter{c.Writer, recorder, tc.cancelFrame, cancel}
			info := &relaycommon.RelayInfo{UserId: tc.user, IsStream: true, StartTime: time.Now(), RelayFormat: types.RelayFormatClaude, ChannelMeta: &relaycommon.ChannelMeta{}}
			info.SetEstimatePromptTokens(99999)
			reader, writer := io.Pipe()
			defer reader.Close()
			defer writer.Close()
			go func() {
				_, _ = io.WriteString(writer, "data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_cancel\",\"usage\":"+tc.start+"}}\n\n")
				_, _ = io.WriteString(writer, "data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"Hello, this must not become estimated token usage.\"}}\n\n")
				if tc.end != "" {
					_, _ = io.WriteString(writer, "data: "+tc.end+"\n\n")
				}
			}()
			usage, err := ClaudeStreamHandler(c, &http.Response{Body: reader}, info)
			if !tc.wantCharge {
				require.NotNil(t, err)
				require.Nil(t, usage)
				return
			}
			require.Nil(t, err)
			require.Equal(t, tc.wantOutput, usage.CompletionTokens)
			if tc.name == "early" {
				require.Equal(t, 100, usage.PromptTokens)
				require.Equal(t, 200, usage.PromptTokensDetails.CachedTokens)
				require.Equal(t, 10, usage.ClaudeCacheCreation5mTokens)
				require.Equal(t, 20, usage.ClaudeCacheCreation1hTokens)
				require.Equal(t, "upstream_reported_partial", usage.UsageSource)
				require.NotNil(t, info.CanceledStreamUsage)
			} else {
				require.Equal(t, "upstream_reported", usage.UsageSource)
				require.Equal(t, "msg_cancel", func() string { _, id := info.StreamStatus.TerminalUsage(); return id }())
			}
		})
	}
}

func TestClaudeReportedUsageRejectsFractionalCountsAndPreservesZeros(t *testing.T) {
	for _, raw := range []string{`{"input_tokens":1.5}`, `{"input_tokens":null}`, `{"input_tokens":"100"}`} {
		state := &ClaudeResponseInfo{}
		captureClaudeReportedUsage(&relaycommon.RelayInfo{}, state, `{"message":{"id":"msg","usage":`+raw+`}}`, &dto.ClaudeResponse{Type: "message_start"})
		require.True(t, state.reportedUsageInvalid)
	}
}

func TestClaudeReportedTerminalStillForwardsMessageStop(t *testing.T) {
	old := constant.StreamingTimeout
	constant.StreamingTimeout = 5
	t.Cleanup(func() { constant.StreamingTimeout = old })
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest("POST", "/v1/messages", nil)
	info := &relaycommon.RelayInfo{UserId: 123, IsStream: true, StartTime: time.Now(), RelayFormat: types.RelayFormatClaude, ChannelMeta: &relaycommon.ChannelMeta{}}
	frames := "data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_complete\",\"usage\":{\"input_tokens\":100,\"output_tokens\":1}}}\n\n" +
		"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":40}}\n\n" +
		"data: {\"type\":\"message_stop\"}\n\n"
	usage, err := ClaudeStreamHandler(c, &http.Response{Body: io.NopCloser(strings.NewReader(frames))}, info)
	require.Nil(t, err)
	require.Equal(t, 40, usage.CompletionTokens)
	require.Contains(t, recorder.Body.String(), `"type":"message_stop"`)
}
