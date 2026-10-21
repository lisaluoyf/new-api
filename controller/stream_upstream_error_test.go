package controller

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestStartedStreamErrorContainsSafeDiagnosticOnce(t *testing.T) {
	for _, format := range []types.RelayFormat{types.RelayFormatOpenAI, types.RelayFormatClaude, types.RelayFormatOpenAIResponses} {
		t.Run(string(format), func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
			c.Set("channel_name", "HiddenRelay")
			c.Set("original_model", "public-model")
			c.Set(common.RequestIdKey, "gateway-request-1")
			c.Writer.WriteHeader(200)
			c.Writer.WriteHeaderNow()
			failure := types.WithOpenAIError(types.OpenAIError{Code: "HiddenRelay.InvalidImage", Type: "HiddenRelay.Error", Message: "HiddenRelay rejected image[0]"}, 422)
			failure.UpstreamError = common.NewPublicUpstreamError(422, []byte(`{"provider":"HiddenRelay","error":{"message":"HiddenRelay requires image/jpeg","details":{"actual":"video/mp4","api_key":"secret-value"}}}`), nil)
			writeStartedStreamError(c, format, failure)
			body := w.Body.String()
			require.Contains(t, body, "data:")
			require.Contains(t, body, "upstream_error")
			require.Contains(t, body, "image/jpeg")
			require.Contains(t, body, "video/mp4")
			require.NotContains(t, body, "HiddenRelay")
			require.NotContains(t, body, "secret-value")
			require.NotContains(t, body, "stream ended unexpectedly")
			require.Equal(t, 200, w.Code, "cannot rewrite status after stream begins")
			writeStartedStreamError(c, format, failure)
			require.Equal(t, body, w.Body.String())
			require.Equal(t, "HiddenRelay.InvalidImage", string(failure.GetErrorCode()), "do not alter internal classifications")
			view := service.PublicRelayErrorView(c, failure).ToOpenAIError()
			detail, _ := common.Marshal(view.UpstreamError)
			for _, line := range strings.Split(body, "\n") {
				if strings.HasPrefix(line, "data: ") {
					data := strings.TrimPrefix(line, "data: ")
					actual := gjson.Get(data, "error.upstream_error")
					if !actual.Exists() {
						actual = gjson.Get(data, "upstream_error")
					}
					require.JSONEq(t, string(detail), actual.Raw)
				}
			}
		})
	}
}

func TestStartedStreamErrorDoesNotWriteAfterClientCancellation(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	requestContext, cancel := context.WithCancel(context.Background())
	cancel()
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil).WithContext(requestContext)
	writeStartedStreamError(c, types.RelayFormatOpenAI, types.NewOpenAIError(errors.New("failure"), types.ErrorCodeBadResponseBody, 502))
	require.Empty(t, w.Body.String())
}
