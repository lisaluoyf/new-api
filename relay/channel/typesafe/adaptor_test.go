package typesafe

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestNativeResponseAndUsage(t *testing.T) {
	var request dto.TypeSafeRequest
	require.NoError(t, common.Unmarshal([]byte(`{"model":"jev-latest","state":"x","questions":{"a":{"type":"noul","instructions":"x"}}}`), &request))
	body := `{"model":"jev-1.13.0","answers":{"a":{"type":"noul","noul":0}},"usage":{"input_tokens":296,"output_tokens":20}}`
	usage, err := ParseResponse([]byte(body), &request)
	require.NoError(t, err)
	require.Equal(t, 296, usage.PromptTokens)
	require.Equal(t, 20, usage.CompletionTokens)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	info := &relaycommon.RelayInfo{Request: &request}
	_, apiErr := (&Adaptor{}).DoResponse(c, &http.Response{Body: io.NopCloser(strings.NewReader(body))}, info)
	require.Nil(t, apiErr)
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, body, w.Body.String())
	for _, bad := range []string{`{}`, `<html>oops</html>`, `{"error":"failed"}`, strings.Replace(body, `"input_tokens":296`, `"input_tokens":-1`, 1), strings.Replace(body, `"input_tokens":296,`, ``, 1), strings.Replace(body, `"a":`, `"b":`, 1)} {
		_, err = ParseResponse([]byte(bad), &request)
		require.Error(t, err)
	}
}

func TestNativeURLAndCredentials(t *testing.T) {
	a := &Adaptor{}
	info := &relaycommon.RelayInfo{RelayFormat: types.RelayFormatTypeSafe, ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://api.typesafe.ai/", ApiKey: "upstream-secret", ChannelType: constant.ChannelTypeTypeSafe}}
	url, err := a.GetRequestURL(info)
	require.NoError(t, err)
	require.Equal(t, "https://api.typesafe.ai/v1/systemone", url)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/systemone", nil)
	c.Request.Header.Set("Authorization", "Bearer customer-secret")
	h := make(http.Header)
	require.NoError(t, a.SetupRequestHeader(c, &h, info))
	require.Equal(t, "Bearer upstream-secret", h.Get("Authorization"))
	info.RelayFormat = types.RelayFormatOpenAI
	_, err = a.GetRequestURL(info)
	require.Error(t, err)
}

func TestTypeSafeTransportPreservesNativeBody(t *testing.T) {
	service.InitHttpClient()
	body := `{"model":"jev-latest","state":{"count":0},"questions":{"a":{"type":"noul","instructions":"Active?","speculative":false}}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/systemone", r.URL.Path)
		require.Equal(t, "POST", r.Method)
		require.Equal(t, "Bearer upstream-secret", r.Header.Get("Authorization"))
		received, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.JSONEq(t, body, string(received))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"jev-1.13.0","answers":{"a":{"type":"noul","noul":0}},"usage":{"input_tokens":100,"output_tokens":20}}`))
	}))
	defer srv.Close()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/systemone", strings.NewReader(body))
	var request dto.TypeSafeRequest
	require.NoError(t, common.Unmarshal([]byte(body), &request))
	info := &relaycommon.RelayInfo{Request: &request, RelayFormat: types.RelayFormatTypeSafe,
		ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: srv.URL, ApiKey: "upstream-secret", ChannelType: constant.ChannelTypeTypeSafe}}
	a := &Adaptor{}
	resp, err := a.DoRequest(c, info, strings.NewReader(body))
	require.NoError(t, err)
	usage, apiErr := a.DoResponse(c, resp.(*http.Response), info)
	require.Nil(t, apiErr)
	require.Equal(t, 100, usage.(*dto.Usage).PromptTokens)
}
