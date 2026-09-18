package openai

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestImageEditsGenerationAdaptorSendsJSON(t *testing.T) {
	service.InitHttpClient()
	for _, id := range []int{59, 81, 149} {
		var received dto.ImageRequest
		var receivedPath, receivedType string
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			receivedPath = r.URL.Path
			receivedType = r.Header.Get("Content-Type")
			raw, _ := io.ReadAll(r.Body)
			_ = common.Unmarshal(raw, &received)
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"data":[{"url":"https://example.test/result.png"}]}`)
		}))
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("POST", "/v1/images/edits", nil)
		c.Request.Header.Set("Content-Type", "application/json")
		format := dto.GptImage2SizeFormatAspectRatioWithResolution
		if id == 149 {
			format = dto.GptImage2SizeFormatPixelDimensions
		}
		info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeImagesEdits, OriginModelName: "gpt-image-2", RequestURLPath: "/v1/images/edits", ChannelMeta: &relaycommon.ChannelMeta{ChannelId: id, ChannelType: constant.ChannelTypeOpenAI, ChannelBaseUrl: upstream.URL, ChannelOtherSettings: dto.ChannelOtherSettings{GptImage2Capabilities: &dto.GptImage2Capabilities{SizeFormat: format}}}}
		a := &Adaptor{}
		a.Init(info)
		var req dto.ImageRequest
		require.NoError(t, common.Unmarshal([]byte(`{"model":"gpt-image-2-official","prompt":"edit","image":"data:image/png;base64,aGVsbG8=","resolution":"2k","size":"1:1"}`), &req))
		converted, err := a.ConvertImageRequest(c, info, req)
		require.NoError(t, err)
		raw, err := common.Marshal(converted)
		require.NoError(t, err)
		resp, err := a.DoRequest(c, info, bytes.NewReader(raw))
		require.NoError(t, err)
		resp.(*http.Response).Body.Close()
		upstream.Close()
		require.Equal(t, "/v1/images/generations", receivedPath)
		require.Equal(t, "application/json", receivedType)
		require.Equal(t, "gpt-image-2-official", received.Model)
		require.Equal(t, []string{"data:image/png;base64,aGVsbG8="}, received.ImageUrls)
		if id == 149 {
			require.Equal(t, "2048x2048", received.Size)
			require.Empty(t, received.Resolution)
		} else {
			require.Equal(t, "1:1", received.Size)
			require.Equal(t, "2k", received.Resolution)
		}
		require.Equal(t, relayconstant.RelayModeImagesEdits, info.RelayMode)
		require.Equal(t, "/v1/images/edits", info.RequestURLPath)
		info.ChannelId = 102
		url, err := a.GetRequestURL(info)
		require.NoError(t, err)
		require.Equal(t, upstream.URL+"/v1/images/edits", url)
	}
}
