package claude_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/QuantumNous/new-api/relay/channel/claude"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// Exercise the real adaptor's upstream usage through the real tiered settlement
// normalizer and frozen wallet prices. Text/cache/reasoning must be charged once.
func TestResponsesClaudeBillingEndToEnd(t *testing.T) {
	old := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = old })
	for _, stream := range []bool{false, true} {
		for _, format := range []types.RelayFormat{types.RelayFormatClaude, types.RelayFormatOpenAI, types.RelayFormatOpenAIResponses} {
			t.Run(string(format)+map[bool]string{false: "/json", true: "/sse"}[stream], func(t *testing.T) {
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
				info := &relaycommon.RelayInfo{IsStream: stream, StartTime: time.Now(), OriginModelName: "claude-opus-5-5", RelayFormat: format, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "claude-opus-5-5"}}
				u := `{"input_tokens":100,"output_tokens":40,"cache_read_input_tokens":200,"cache_creation_input_tokens":30,"cache_creation":{"ephemeral_5m_input_tokens":10,"ephemeral_1h_input_tokens":20},"output_tokens_details":{"thinking_tokens":15}}`
				body := `{"id":"msg_bill","type":"message","role":"assistant","content":[{"type":"text","text":"Hello"}],"stop_reason":"end_turn","usage":` + u + `}`
				if stream {
					body = "data: " + strings.Join([]string{`{"type":"message_start","message":{"id":"msg_bill","usage":` + u + `}}`, `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`, `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}`, `{"type":"content_block_stop","index":0}`, `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":40}}`, `{"type":"message_stop"}`}, "\n\ndata: ") + "\n\n"
				}
				usage, err := (&claude.Adaptor{}).DoResponse(c, &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, info)
				require.Nil(t, err)
				// A cached context of 330 selects the long tier even though non-cache p=100.
				expr := `len > 300 ? tier("long", p * 4 + c * 20 + cr * 0.4 + cc * 5 + cc1h * 8) : tier("base", p * 2 + c * 10 + cr * 0.2 + cc * 2.5 + cc1h * 4)`
				actual := usage.(*dto.Usage)
				inclusive, _ := service.ResolveInputTokensIncludeCache(info, info.OriginModelName, actual)
				require.False(t, inclusive)
				params := service.BuildTieredTokenParams(actual, inclusive, billingexpr.UsedVars(expr))
				require.Equal(t, 100.0, params.P)
				require.Equal(t, 40.0, params.C)
				require.Equal(t, 330.0, params.Len)
				require.Equal(t, 200.0, params.CR)
				require.Equal(t, 10.0, params.CC)
				require.Equal(t, 20.0, params.CC1h)
				info.TieredBillingSnapshot = &billingexpr.BillingSnapshot{BillingMode: "tiered_expr", ExprString: expr, ExprHash: billingexpr.ExprHashString(expr), GroupRatio: 1, QuotaPerUnit: 500000}
				ok, quota, result := service.TryTieredSettle(info, params)
				require.True(t, ok)
				require.Equal(t, "long", result.MatchedTier)
				// (100*4 + 40*20 + 200*.4 + 10*5 + 20*8) / 1M * 500K.
				require.Equal(t, 745, quota)
			})
		}
	}
}
