package service

import (
	"encoding/json"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"net/http/httptest"
	"testing"
)

func TestSeedreamImageCharge(t *testing.T) {
	p := ratio_setting.ImageModelPricingDetails{BasePrice: 0.045, Prices: map[string]float64{
		"Image Output": 0.045, "High-Resolution Image Output": 0.09, "Layer Image Output": 0.0225, "High-Resolution Layer Image Output": 0.045, "Image Input (after first)": 0.003,
	}}
	for _, tc := range []struct {
		name   string
		sizes  []string
		inputs int
		layers bool
		want   float64
	}{
		{"first reference free", []string{"1872x1248"}, 1, false, 0.045},
		{"high resolution", []string{"2048x2048"}, 0, false, 0.09},
		{"long edge does not set tier", []string{"2048x1024"}, 2, false, 0.048},
		{"exact threshold", []string{"1800x1450"}, 0, false, 0.045},
		{"above threshold", []string{"1801x1450"}, 0, false, 0.09},
		{"mixed layers include base", []string{"2048x2048", "1024x1024", "1872x1248"}, 1, true, 0.09},
		{"ten references", []string{"1024x1024"}, 10, false, 0.072},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := []map[string]string{}
			for _, s := range tc.sizes {
				data = append(data, map[string]string{"url": "https://example.test/image.png", "size": s})
			}
			body, err := common.Marshal(map[string]interface{}{"data": data, "usage": map[string]int{"input_images": tc.inputs, "generated_images": len(data)}})
			require.NoError(t, err)
			got, _, err := SeedreamImageCharge(body, tc.layers, p)
			require.NoError(t, err)
			require.InDelta(t, tc.want, got, 1e-10)
		})
	}
	for _, body := range []string{`{"data":[]}`, `{"data":[{"url":"x","size":"bad"}],"usage":{"generated_images":1}}`, `{"data":[{"url":"x","size":"1024x1024"}],"usage":{"generated_images":2}}`} {
		_, _, err := SeedreamImageCharge([]byte(body), false, p)
		require.Error(t, err)
	}
	// The base ratio must not use the generic longest-edge resolution tiers.
	req := dto.ImageRequest{Model: dto.Seedream5ProModel, Size: "2048x2048"}
	require.Equal(t, "Image Output", req.EffectiveResolutionTier())
	p.Prices["Image Output"] = 0.06
	amount, _, err := SeedreamImageCharge([]byte(`{"data":[{"url":"x","size":"1024x1024"}],"usage":{"generated_images":1}}`), false, p)
	require.NoError(t, err)
	require.InDelta(t, 0.06, amount, 1e-10)
}

func TestApplySeedreamImageBillingPreservesChannelPrice(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	req := &dto.ImageRequest{Model: dto.Seedream5ProModel, Size: "2K", Extra: map[string]json.RawMessage{"layer_decomposition": json.RawMessage(`true`)}}
	info := &relaycommon.RelayInfo{OriginModelName: dto.Seedream5ProModel, Request: req, PriceData: types.PriceData{UsePrice: true, ModelPrice: 0.09}}
	SetImageRequestDataOnContext(c, req)
	err := ApplySeedreamImageBilling(c, info, []byte(`{"data":[{"url":"x","size":"2048x2048"},{"url":"y","size":"1024x1024"}],"usage":{"input_images":1,"generated_images":2}}`))
	require.NoError(t, err)
	require.InDelta(t, 0.09, info.PriceData.ModelPrice, 1e-10)
	require.InDelta(t, 1.5, info.PriceData.OtherRatios["seedream_images"], 1e-10)
	require.Equal(t, float64(1), info.PriceData.OtherRatios["n"])
	require.Equal(t, 2, ImageRequestDataFromContext(c)["actual_image_count"])
}
