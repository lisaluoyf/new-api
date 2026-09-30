package hailuo

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestH3GenerationAliasesPreserveNonDefaultSpecs(t *testing.T) {
	for _, path := range []string{"/v1/video/generations", "/v1/videos/generations"} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(`{"model":"MiniMax-H3","prompt":"test","duration":8,"size":"2K","aspect_ratio":"9:16"}`))
		c.Request.Header.Set("Content-Type", "application/json")
		a := &H3TaskAdaptor{}
		info := &relaycommon.RelayInfo{TaskRelayInfo: &relaycommon.TaskRelayInfo{}}
		require.Nil(t, a.ValidateRequestAndSetAction(c, info))
		reader, err := a.BuildRequestBody(c, info)
		require.NoError(t, err)
		var payload h3CreateRequest
		require.NoError(t, common.DecodeJson(reader, &payload))
		require.Equal(t, "2K", payload.Resolution)
		require.Equal(t, "9:16", payload.Ratio)
		require.Equal(t, 8, payload.Duration)
		require.Equal(t, 8.0, a.EstimateBilling(c, info)["seconds"])
	}
}

func TestH3GenerationAliasesRejectConflictingSpecs(t *testing.T) {
	for _, path := range []string{"/v1/video/generations", "/v1/videos/generations"} {
		for _, extra := range []string{`"resolution":"720p"`, `"resolution":null`, `"size":"2K","resolution":"768P"`, `"ratio":"16:9","aspect_ratio":"9:16"`, `"aspect_ratio":[]`, `"duration":3`} {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(`{"model":"MiniMax-H3","prompt":"test",`+extra+`}`))
			c.Request.Header.Set("Content-Type", "application/json")
			e := (&H3TaskAdaptor{}).ValidateRequestAndSetAction(c, &relaycommon.RelayInfo{TaskRelayInfo: &relaycommon.TaskRelayInfo{}})
			require.NotNil(t, e, extra)
			require.Equal(t, 400, e.StatusCode, extra)
		}
	}
}
