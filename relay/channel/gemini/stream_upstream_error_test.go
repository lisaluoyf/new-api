package gemini

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/stretchr/testify/require"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestResponsesFailedEventCarriesCapturedDiagnostic(t *testing.T) {
	old := constant.StreamingTimeout
	constant.StreamingTimeout = 5
	t.Cleanup(func() { constant.StreamingTimeout = old })
	c, rec, info := geminiResponsesFixture(true)
	c.Set("channel_name", "HiddenRelay")
	body := "data: " + `{"candidates":[{"content":{"parts":[{"text":"partial answer"}]}}]}` + "\n\n" + "data: " + `{"error":{"code":429,"message":"HiddenRelay capacity exhausted","details":{"retry_after":17,"secret_key":"secret-value"}}}` + "\n\n"
	_, failure := GeminiResponsesHandler(c, info, &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))})
	require.NotNil(t, failure)
	require.NotNil(t, failure.UpstreamError)
	require.True(t, c.GetBool("stream_terminal_error_written"))
	wire := rec.Body.String()
	require.Equal(t, 1, strings.Count(wire, "event: response.failed"))
	require.Contains(t, wire, "capacity exhausted")
	require.Contains(t, wire, `"retry_after":17`)
	require.NotContains(t, wire, "HiddenRelay")
	require.NotContains(t, wire, "secret-value")
	detail, _ := common.Marshal(failure.UpstreamError)
	require.Contains(t, wire, string(detail))
}
