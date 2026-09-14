package controller

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relay/channel"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func streamRetryContext() (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	c.Set(string(constant.ContextKeyIsStream), true)
	c.Set(common.RequestIdKey, "heartbeat-regression")
	c.Writer = helper.NewStreamResponseWriter(c.Writer)
	helper.SetEventStreamHeaders(c)
	return c, recorder
}

func TestStreamRetryAfterHeartbeat(t *testing.T) {
	for _, status := range []int{400, 500, 502, 503} {
		c, recorder := streamRetryContext()
		require.NoError(t, helper.PingData(c))
		err := types.NewOpenAIError(errors.New("upstream failed"), types.ErrorCodeBadResponseStatusCode, status)
		decision := evaluateRetry(c, err, 0, 1)
		require.True(t, decision.ShouldRetry, "status %d: %s", status, decision.Reason)
		setRetryDecision(c, decision)
		detail, ok := getRetryDecision(c)
		require.True(t, ok)
		require.Equal(t, "heartbeat_only", detail["stream_output_state"])
		require.Equal(t, status, detail["status_code"])
		require.NoError(t, helper.StringData(c, `{"choices":[{"delta":{"content":"fallback result"}}]}`))
		helper.Done(c)
		require.Equal(t, http.StatusOK, recorder.Code)
		require.Contains(t, recorder.Body.String(), "fallback result")
		require.NotContains(t, recorder.Body.String(), "error")
	}
}

func TestStreamRetryStopsAfterBusinessOutput(t *testing.T) {
	for _, frame := range []string{
		"data: {\"choices\":[{\"delta\":{\"role\":\"assistant\"}}]}\n\n",
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[]}}]}\n\n",
		"event: response.created\n",
		"event: message_start\n",
		"data: [DONE]\n\n",
	} {
		c, _ := streamRetryContext()
		require.NoError(t, helper.PingData(c))
		_, err := c.Writer.WriteString(frame)
		require.NoError(t, err)
		decision := evaluateRetry(c, types.NewError(errors.New("stream failed"), types.ErrorCodeBadResponse), 0, 1)
		require.False(t, decision.ShouldRetry, frame)
		require.Equal(t, "stream_already_started", decision.Reason)
	}
}

func TestStreamRetryExhaustionAndCancellation(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		c, recorder := streamRetryContext()
		require.NoError(t, helper.PingData(c))
		if canceled {
			ctx, cancel := context.WithCancel(c.Request.Context())
			c.Request = c.Request.WithContext(ctx)
			cancel()
		}
		err := types.NewError(errors.New("failed"), types.ErrorCodeBadResponse)
		decision := evaluateRetry(c, err, 1, 0)
		require.False(t, decision.ShouldRetry)
		if canceled {
			require.Equal(t, "client_canceled", decision.Reason)
			writeStartedStreamError(c, types.RelayFormatOpenAIResponses, err)
			require.Equal(t, ": PING\n\n", recorder.Body.String())
		} else {
			require.Equal(t, "retry_times_exhausted", decision.Reason)
		}
	}
}

func TestStartedStreamTerminalErrorProtocols(t *testing.T) {
	for _, format := range []types.RelayFormat{types.RelayFormatOpenAI, types.RelayFormatOpenAIResponses, types.RelayFormatClaude, types.RelayFormatGemini} {
		t.Run(string(format), func(t *testing.T) {
			c, recorder := streamRetryContext()
			require.NoError(t, helper.PingData(c))
			err := types.NewError(errors.New("secret upstream detail"), types.ErrorCodeBadResponse)
			writeStartedStreamError(c, format, err)
			writeStartedStreamError(c, format, err)
			body := recorder.Body.String()
			require.True(t, strings.HasSuffix(body, "\n\n"), body)
			require.Equal(t, 1, strings.Count(body, "data: "))
			require.NotContains(t, body, "secret upstream detail")
			require.Contains(t, body, "heartbeat-regression")
			require.Equal(t, http.StatusOK, recorder.Code)
			data := strings.SplitN(body, "data: ", 2)[1]
			var payload map[string]interface{}
			require.NoError(t, common.Unmarshal([]byte(strings.TrimSpace(data)), &payload))
			if format == types.RelayFormatOpenAIResponses {
				require.Equal(t, "error", payload["type"])
				require.Equal(t, float64(0), payload["sequence_number"])
				require.NotEmpty(t, payload["code"])
			} else {
				require.NotNil(t, payload["error"])
			}
		})
	}
}

func TestFreeModelRetryHeartbeatAndUnknownWriter(t *testing.T) {
	c, _ := streamRetryContext()
	err := types.NewError(errors.New("failed"), types.ErrorCodeBadResponse)
	require.NoError(t, helper.PingData(c))
	require.True(t, evaluateFreeModelRetry(c, err, 0, 1).ShouldRetry)
	// Untracked writers retain the conservative legacy rule.
	c.Writer = c.Writer.(*helper.StreamResponseWriter).ResponseWriter
	require.False(t, evaluateRetry(c, err, 0, 1).ShouldRetry)
}

func TestStreamRetryWithDelayedHTTPUpstream(t *testing.T) {
	settings := operation_setting.GetGeneralSetting()
	oldSettings := *settings
	settings.PingIntervalEnabled = true
	settings.PingIntervalSeconds = 1
	t.Cleanup(func() { *settings = oldSettings })
	if service.GetHttpClient() == nil {
		service.InitHttpClient()
	}
	for _, format := range []types.RelayFormat{types.RelayFormatOpenAI, types.RelayFormatOpenAIResponses} {
		for _, fallbackOK := range []bool{true, false} {
			c, recorder := streamRetryContext()
			primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Return an upstream error only after the real request pinger has
				// committed a heartbeat to the downstream response.
				deadline := time.NewTimer(3 * time.Second)
				defer deadline.Stop()
				ticker := time.NewTicker(time.Millisecond)
				defer ticker.Stop()
				for !c.Writer.Written() {
					select {
					case <-ticker.C:
					case <-deadline.C:
						w.WriteHeader(http.StatusGatewayTimeout)
						return
					}
				}
				w.WriteHeader(http.StatusBadRequest)
			}))
			fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !fallbackOK {
					w.WriteHeader(http.StatusBadGateway)
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, "data: {\"fallback\":true}\n\n")
			}))
			func() {
				defer primary.Close()
				defer fallback.Close()
				info := &relaycommon.RelayInfo{IsStream: true, ChannelMeta: &relaycommon.ChannelMeta{}}
				for attempt, upstream := range []string{primary.URL, fallback.URL} {
					req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, upstream, strings.NewReader("{}"))
					require.NoError(t, err)
					resp, err := channel.DoRequest(c, req, info)
					require.NoError(t, err)
					if resp.StatusCode == http.StatusOK {
						_, err = io.Copy(c.Writer, resp.Body)
						require.NoError(t, err)
						require.NoError(t, resp.Body.Close())
						break
					}
					require.NoError(t, resp.Body.Close())
					apiErr := types.NewOpenAIError(errors.New("upstream failed"), types.ErrorCodeBadResponseStatusCode, resp.StatusCode)
					decision := evaluateRetry(c, apiErr, attempt, 1-attempt)
					if attempt == 0 {
						require.Equal(t, http.StatusBadRequest, resp.StatusCode)
						require.Equal(t, "heartbeat_only", helper.StreamOutputState(c))
						require.True(t, decision.ShouldRetry)
					} else {
						require.False(t, decision.ShouldRetry)
						writeStartedStreamError(c, format, apiErr)
					}
				}
			}()
			require.Equal(t, http.StatusOK, recorder.Code)
			if fallbackOK {
				require.Contains(t, recorder.Body.String(), `"fallback":true`)
				require.NotContains(t, recorder.Body.String(), "error")
			} else {
				require.Contains(t, recorder.Body.String(), "error")
				require.True(t, strings.HasSuffix(recorder.Body.String(), "\n\n"))
			}
		}
	}
}
