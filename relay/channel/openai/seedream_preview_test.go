package openai

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestSeedreamResponsePreservesLayerPreviewURLs(t *testing.T) {
	response := `{"data":[{"url":"https://apimaster.ai/imgs/base.png","size":"2048x2048","z_index":0},{"url":"https://apimaster.ai/imgs/layer.png","size":"1024x1024","z_index":1,"name":"circle"}],"usage":{"input_images":1,"generated_images":2}}`
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)
	info := &relaycommon.RelayInfo{
		OriginModelName: dto.Seedream5ProModel,
		RelayMode:       relayconstant.RelayModeImagesGenerations,
		ChannelMeta:     &relaycommon.ChannelMeta{},
		PriceData:       types.PriceData{UsePrice: true, ModelPrice: 0.045},
		Request: &dto.ImageRequest{
			Model: dto.Seedream5ProModel,
			Extra: map[string]json.RawMessage{"layer_decomposition": json.RawMessage(`true`)},
		},
	}
	_, err := OpenaiHandlerWithUsage(c, info, &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(response)),
	})
	require.Nil(t, err)
	require.Equal(t, "https://apimaster.ai/imgs/base.png", c.GetString("image_result_url"))
	require.Equal(t, []string{"https://apimaster.ai/imgs/base.png", "https://apimaster.ai/imgs/layer.png"}, c.GetStringSlice("image_result_urls"))
	require.JSONEq(t, response, w.Body.String())
}
