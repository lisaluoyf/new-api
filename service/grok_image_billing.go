package service

import (
	"fmt"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
)

const GrokImagePricingContextKey = "grok_image_pricing"

// Reference images are charged once per request, not once per output image.
func GrokImageCharge(req *dto.ImageRequest, count int, pricing ratio_setting.ImageModelPricingDetails) (float64, map[string]interface{}, error) {
	if req == nil || count < 1 || req.N == nil || count > int(*req.N) {
		return 0, nil, fmt.Errorf("invalid Grok output image count")
	}
	variant := req.GrokImagePriceVariant()
	outputPrice := pricing.Prices[variant]
	inputPrice := pricing.Prices["Image Input"]
	if pricing.BasePrice <= 0 || outputPrice <= 0 || inputPrice <= 0 {
		return 0, nil, fmt.Errorf("Grok image output/reference prices are not configured")
	}
	total := float64(count)*outputPrice + float64(len(req.ImageUrls))*inputPrice
	return total, map[string]interface{}{
		"base_variant": variant, "layer_decomposition": false,
		"input_images": len(req.ImageUrls), "generated_images": count,
		"billable_counts": map[string]int{variant: count, "Image Input": len(req.ImageUrls)},
		"base_prices":     pricing.Prices, "base_amount_usd": total,
	}, nil
}

func ApplyGrokImageBilling(c *gin.Context, info *relaycommon.RelayInfo, body []byte) error {
	if !dto.IsGrokImage20(info.OriginModelName) {
		return nil
	}
	req, ok := info.Request.(*dto.ImageRequest)
	if !ok || !info.PriceData.UsePrice {
		return fmt.Errorf("Grok image request or pricing missing")
	}
	var response dto.ImageResponse
	if err := common.Unmarshal(body, &response); err != nil {
		return err
	}
	for _, image := range response.Data {
		if image.Url == "" && image.B64Json == "" {
			return fmt.Errorf("Grok output has no image")
		}
	}
	snapshot, exists := c.Get(GrokImagePricingContextKey)
	pricing, ok := snapshot.(ratio_setting.ImageModelPricingDetails)
	if !exists || !ok {
		return fmt.Errorf("Grok image price snapshot missing")
	}
	total, details, err := GrokImageCharge(req, len(response.Data), pricing)
	if err != nil {
		return err
	}
	// ModelPrice already includes the output variant and channel coefficients.
	info.PriceData.AddOtherRatio("n", 1)
	info.PriceData.AddOtherRatio("grok_images", total/pricing.Prices[req.GrokImagePriceVariant()])
	details["base_units"] = total / pricing.BasePrice
	c.Set("grok_image_billing", details)
	if data := ImageRequestDataFromContext(c); data != nil {
		data["actual_image_count"] = len(response.Data)
		data["input_images"] = len(req.ImageUrls)
	}
	return nil
}
