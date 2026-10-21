package claude

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestResponsesFailedEventPreservesSafeUpstreamErrorFrame(t *testing.T) {
	c, rec, info := responsesClaudeContext(t, true)
	c.Set("channel_name", "HiddenRelay")
	events := []string{responsesClaudeEvents("end_turn")[0], `{"type":"error","provider":"HiddenRelay","error":{"type":"overloaded_error","message":"HiddenRelay: capacity exhausted for this request","details":{"retry_after":17,"secret_key":"secret-value"}}}`}
	response := &http.Response{StatusCode: 200, Header: http.Header{"X-Request-Id": []string{"opaque-stream-request"}}, Body: io.NopCloser(strings.NewReader(responsesClaudeSSE(events)))}
	usage, failure := ClaudeResponsesHandler(c, response, info)
	require.Nil(t, usage)
	require.NotNil(t, failure)
	require.NotNil(t, failure.UpstreamError)
	require.Equal(t, "opaque-stream-request", failure.UpstreamError.RequestID)
	wire := rec.Body.String()
	require.Equal(t, 1, strings.Count(wire, "event: response.failed"))
	require.NotContains(t, wire, "event: response.completed")
	require.Contains(t, wire, "capacity exhausted")
	require.Contains(t, wire, `"retry_after":17`)
	require.Contains(t, wire, "upstream_error")
	require.NotContains(t, wire, "HiddenRelay")
	require.NotContains(t, wire, "secret-value")
	detail, _ := common.Marshal(failure.UpstreamError)
	require.Contains(t, wire, string(detail))
}
