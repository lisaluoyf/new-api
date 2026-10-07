package gemini

import (
	"context"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type cancelGeminiWriter struct {
	gin.ResponseWriter
	cancel context.CancelFunc
}

func (w *cancelGeminiWriter) Write(p []byte) (int, error) {
	n, e := w.ResponseWriter.Write(p)
	if strings.Contains(string(p), "hello") {
		w.cancel()
		time.Sleep(20 * time.Millisecond)
	}
	return n, e
}

func TestGeminiCancellationSettlesOnlyProviderCounts(t *testing.T) {
	old := constant.StreamingTimeout
	constant.StreamingTimeout = 5
	t.Cleanup(func() { constant.StreamingTimeout = old })
	for _, reported := range []bool{true, false} {
		t.Run(map[bool]string{true: "reported", false: "missing"}[reported], func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil).WithContext(ctx)
			c.Writer = &cancelGeminiWriter{c.Writer, cancel}
			info := &relaycommon.RelayInfo{UserId: 123, IsStream: true, StartTime: time.Now(), RelayFormat: types.RelayFormatOpenAI, OriginModelName: "gemini-test", ChannelMeta: &relaycommon.ChannelMeta{}}
			info.SetEstimatePromptTokens(99999)
			reader, writer := io.Pipe()
			defer reader.Close()
			defer writer.Close()
			payload := `{"candidates":[{"content":{"role":"model","parts":[{"text":"hello"}]}}]`
			if reported {
				payload += `,"usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":3,"totalTokenCount":103,"cachedContentTokenCount":70}`
			}
			payload += "}"
			go func() { _, _ = io.WriteString(writer, "data: "+payload+"\n\n") }()
			u, err := GeminiChatStreamHandler(c, info, &http.Response{StatusCode: 200, Body: reader})
			if !reported {
				require.NotNil(t, err)
				require.Nil(t, u)
				return
			}
			require.Nil(t, err)
			require.Equal(t, 100, u.PromptTokens)
			require.Equal(t, 3, u.CompletionTokens)
			require.Equal(t, 70, u.PromptTokensDetails.CachedTokens)
			require.Equal(t, "upstream_reported_partial", u.UsageSource)
		})
	}
}

func TestGeminiMalformedUsageCannotAuthorizeCancellation(t *testing.T) {
	for _, raw := range []string{`{"promptTokenCount":null,"totalTokenCount":0}`, `{"promptTokenCount":-1,"totalTokenCount":0}`, `{"promptTokenCount":3,"totalTokenCount":3,"cachedContentTokenCount":4}`, `{"promptTokenCount":3,"totalTokenCount":3,"candidatesTokenCount":1.5}`} {
		var metadata dto.GeminiUsageMetadata
		_ = common.UnmarshalJsonStr(raw, &metadata)
		require.Nil(t, reportedGeminiUsage(`{"usageMetadata":`+raw+`}`, metadata))
	}
}
