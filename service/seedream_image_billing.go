package service

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
)

// Seedream bills each output (including the base image in decomposition) by
// its own pixel area. Input references after the first are charged separately.
const SeedreamStandardMaxPixels int64 = 2610000

func seedreamPixels(size string) (int64, error) {
	parts := strings.Split(strings.ToLower(strings.TrimSpace(size)), "x")
	if len(parts) != 2 {
		return 0, fmt.Errorf("invalid Seedream output size %q", size)
	}
	w, ew := strconv.ParseInt(parts[0], 10, 64)
	h, eh := strconv.ParseInt(parts[1], 10, 64)
	if ew != nil || eh != nil || w <= 0 || h <= 0 || w > 100000 || h > 100000 {
		return 0, fmt.Errorf("invalid Seedream output size %q", size)
	}
	return w * h, nil
}

func SeedreamImageCharge(body []byte, layers bool, pricing ratio_setting.ImageModelPricingDetails) (float64, map[string]interface{}, error) {
	var response struct {
		Data []struct {
			Size string `json:"size"`
			URL  string `json:"url"`
			B64  string `json:"b64_json"`
		} `json:"data"`
		Usage struct {
			InputImages     int `json:"input_images"`
			GeneratedImages int `json:"generated_images"`
		} `json:"usage"`
	}
	if err := common.Unmarshal(body, &response); err != nil {
		return 0, nil, err
	}
	if len(response.Data) == 0 || response.Usage.GeneratedImages != len(response.Data) || response.Usage.InputImages < 0 {
		return 0, nil, fmt.Errorf("invalid Seedream output count or usage")
	}
	counts := map[string]int{}
	for _, item := range response.Data {
		if item.URL == "" && item.B64 == "" {
			return 0, nil, fmt.Errorf("Seedream output has no image")
		}
		pixels, err := seedreamPixels(item.Size)
		if err != nil {
			return 0, nil, err
		}
		variant := "Image Output"
		if layers {
			variant = "Layer Image Output"
		}
		if pixels > SeedreamStandardMaxPixels {
			variant = "High-Resolution " + variant
		}
		counts[variant]++
	}
	if response.Usage.InputImages > 1 {
		counts["Image Input (after first)"] = response.Usage.InputImages - 1
	}
	total := 0.0
	for variant, count := range counts {
		price, ok := pricing.Prices[variant]
		if !ok || price <= 0 {
			return 0, nil, fmt.Errorf("missing Seedream image price for %s", variant)
		}
		total += price * float64(count)
	}
	return total, map[string]interface{}{"layer_decomposition": layers, "input_images": response.Usage.InputImages, "generated_images": response.Usage.GeneratedImages, "billable_counts": counts, "base_amount_usd": total, "base_prices": pricing.Prices}, nil
}

func ApplySeedreamImageBilling(c *gin.Context, info *relaycommon.RelayInfo, body []byte) error {
	if !dto.IsSeedream5Pro(info.OriginModelName) {
		return nil
	}
	req, ok := info.Request.(*dto.ImageRequest)
	if !ok {
		return fmt.Errorf("missing Seedream image request")
	}
	layers := false
	if raw := req.Extra["layer_decomposition"]; len(raw) > 0 {
		if err := common.Unmarshal(raw, &layers); err != nil {
			return err
		}
	}
	pricing, ok := ratio_setting.GetImageModelPricingDetails(info.OriginModelName)
	if !ok || pricing.BasePrice <= 0 || !info.PriceData.UsePrice {
		return fmt.Errorf("Seedream image pricing is not configured")
	}
	total, details, err := SeedreamImageCharge(body, layers, pricing)
	if err != nil {
		return err
	}
	// The request's Image Output variant reserves exactly the base price. Keep
	// all channel/user coefficients; apply actual usage once, independently of n.
	info.PriceData.AddOtherRatio("n", 1)
	info.PriceData.AddOtherRatio("seedream_images", total/pricing.BasePrice)
	if requestData := ImageRequestDataFromContext(c); requestData != nil {
		requestData["actual_image_count"] = details["generated_images"]
		requestData["input_images"] = details["input_images"]
		requestData["layer_decomposition"] = layers
	}
	c.Set("seedream_image_billing", details)
	return nil
}
