package service

import (
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

func grokTestPricing() ratio_setting.ImageModelPricingDetails {
	return ratio_setting.ImageModelPricingDetails{BasePrice: .04, BaseVariant: "1K low", Prices: map[string]float64{"1K low": .04, "1K medium": .06, "2K low": .06, "2K medium": .08, "Image Input": .01}}
}
func TestGrokImageCharge(t *testing.T) {
	p := grokTestPricing()
	for _, tc := range []struct {
		resolution, quality string
		count, refs         int
		want                float64
	}{
		{"1k", "low", 1, 0, .04}, {"1k", "medium", 1, 0, .06}, {"2k", "low", 1, 0, .06}, {"2k", "medium", 1, 0, .08},
		{"2k", "medium", 2, 1, .17}, {"2k", "medium", 1, 2, .10},
	} {
		req := &dto.ImageRequest{Resolution: tc.resolution, Quality: tc.quality, N: common.GetPointer(uint(2)), ImageUrls: make([]string, tc.refs)}
		amount, _, err := GrokImageCharge(req, tc.count, p)
		require.NoError(t, err)
		require.InDelta(t, tc.want, amount, 1e-10)
	}
	req := &dto.ImageRequest{N: common.GetPointer(uint(2)), ImageUrls: []string{"ref"}, Quality: "low"}
	p.Prices["1K low"] = .05
	p.Prices["Image Input"] = .02
	amount, _, err := GrokImageCharge(req, 2, p)
	require.NoError(t, err)
	require.InDelta(t, .12, amount, 1e-10)
	for _, n := range []int{0, 3} {
		_, _, err = GrokImageCharge(req, n, p)
		require.Error(t, err)
	}
	delete(p.Prices, "Image Input")
	_, _, err = GrokImageCharge(req, 1, p)
	require.Error(t, err)
}
func TestApplyGrokImageBillingActualCountAndSnapshot(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	req := &dto.ImageRequest{Model: dto.GrokImage20Model, N: common.GetPointer(uint(2)), Resolution: "2k", Quality: "medium", ImageUrls: []string{"ref"}}
	info := &relaycommon.RelayInfo{OriginModelName: dto.GrokImage20Model, Request: req, PriceData: types.PriceData{UsePrice: true, ModelPrice: .0768}}
	SetImageRequestDataOnContext(c, req)
	require.Error(t, ApplyGrokImageBilling(c, info, []byte(`{"data":[{"url":"x"}]}`)))
	c.Set(GrokImagePricingContextKey, grokTestPricing())
	require.NoError(t, ApplyGrokImageBilling(c, info, []byte(`{"data":[{"url":"x"}]}`)))
	require.InDelta(t, .0768, info.PriceData.ModelPrice, 1e-10)
	require.InDelta(t, 1.125, info.PriceData.OtherRatios["grok_images"], 1e-10)
	require.Equal(t, float64(1), info.PriceData.OtherRatios["n"])
	require.Equal(t, 1, ImageRequestDataFromContext(c)["actual_image_count"])
	detail, _ := c.Get("grok_image_billing")
	require.InDelta(t, .09, detail.(map[string]interface{})["base_amount_usd"], 1e-10)
	for _, body := range []string{`{"data":[]}`, `{"data":[{}]}`, `{"data":[{"url":"x"},{"url":"y"},{"url":"z"}]}`} {
		require.Error(t, ApplyGrokImageBilling(c, info, []byte(body)))
	}
}
