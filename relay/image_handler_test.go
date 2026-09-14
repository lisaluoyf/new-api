package relay

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestImageHelperSeedreamInvalidParameter(t *testing.T) {
	service.InitHttpClient()
	for _, tc := range []struct {
		name, model, code string
		status            int
		skipRetry         bool
	}{
		{"invalid parameters", dto.Seedream5ProModel, "InvalidParameter", 400, true},
		{"upstream failure", dto.Seedream5ProModel, "InternalError", 500, false},
		{"rate limit", dto.Seedream5ProModel, "TooManyRequests", 429, false},
		{"other model", "doubao-seedream-4-5-251128", "InvalidParameter", 400, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				fmt.Fprintf(w, `{"error":{"code":%q,"message":"test upstream error","type":"invalid_request_error"}}`, tc.code)
			}))
			defer upstream.Close()
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)
			common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeVolcEngine)
			common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, upstream.URL)
			common.SetContextKey(c, constant.ContextKeyOriginalModel, tc.model)
			info := &relaycommon.RelayInfo{
				OriginModelName: tc.model,
				RelayMode:       relayconstant.RelayModeImagesGenerations,
				Request:         &dto.ImageRequest{Model: tc.model, Prompt: "test", Size: "1K"},
			}
			err := ImageHelper(c, info)
			require.NotNil(t, err)
			require.Equal(t, tc.status, err.StatusCode)
			require.Equal(t, types.ErrorCode(tc.code), err.GetErrorCode())
			require.Contains(t, err.Error(), "test upstream error")
			require.Equal(t, tc.skipRetry, types.IsSkipRetryError(err))
		})
	}
}
