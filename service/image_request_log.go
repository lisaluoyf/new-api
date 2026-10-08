package service

import (
	"strings"

	"github.com/QuantumNous/new-api/dto"
)

const imageRequestDataContextKey = "image_request_data"

// SetImageRequestDataOnContext stores sanitized request params for usage log preview.
func SetImageRequestDataOnContext(c interface{ Set(string, any) }, req *dto.ImageRequest) {
	if c == nil || req == nil {
		return
	}
	if data := BuildImageRequestDataForLog(req); len(data) > 0 {
		c.Set(imageRequestDataContextKey, data)
	}
}

// ImageRequestDataFromContext reads request params stashed during image relay.
func ImageRequestDataFromContext(c interface{ Get(string) (any, bool) }) map[string]interface{} {
	if c == nil {
		return nil
	}
	raw, ok := c.Get(imageRequestDataContextKey)
	if !ok || raw == nil {
		return nil
	}
	if m, ok := raw.(map[string]interface{}); ok && len(m) > 0 {
		return m
	}
	return nil
}

// ApplyImageBillingLogInfo adds the settled media billing presentation for a
// generic image request. The accounting snapshot remains the source of truth
// for settlement; this only prevents image requests from being rendered as
// token-priced logs when a subscription price path also carries tiered data.
func ApplyImageBillingLogInfo(other map[string]interface{}, modelName string, requestData map[string]interface{}) bool {
	if other == nil || len(requestData) == 0 {
		return false
	}
	imageCount := coerceRequestInt(requestData["actual_image_count"])
	if imageCount <= 0 {
		return false
	}

	pricing, ok := GlobalImageMediaPricingUSD(modelName)
	if !ok || pricing.BasePrice <= 0 {
		return false
	}
	variant, _ := requestData["effective_resolution"].(string)
	variant = strings.TrimSpace(variant)
	if variant == "" {
		variant = pricing.BaseVariant
	}
	unitPrice := pricing.BasePrice
	for name, price := range pricing.Prices {
		if strings.EqualFold(strings.TrimSpace(name), variant) && price > 0 {
			unitPrice = price
			variant = name
			break
		}
	}
	if unitPrice <= 0 {
		return false
	}

	other["billing_mode"] = accountingBillingModeImageCount
	if _, exists := other["image_billing"]; exists {
		return true
	}
	other["model_price"] = unitPrice
	other["image_billing"] = map[string]interface{}{
		"base_variant":        variant,
		"layer_decomposition": false,
		"input_images":        0,
		"generated_images":    imageCount,
		"billable_counts":     map[string]int{variant: imageCount},
		"base_prices":         pricing.Prices,
		"base_amount_usd":     unitPrice * float64(imageCount),
	}
	return true
}

// BuildImageRequestDataForLog returns user-facing request fields for log preview.
func BuildImageRequestDataForLog(req *dto.ImageRequest) map[string]interface{} {
	if req == nil {
		return nil
	}

	imageN := uint(1)
	if req.N != nil && *req.N > 0 {
		imageN = *req.N
	}

	data := map[string]interface{}{
		"model":              strings.TrimSpace(req.Model),
		"prompt":             req.Prompt,
		"n":                  imageN,
		"actual_image_count": imageN,
	}
	if size := strings.TrimSpace(req.Size); size != "" {
		data["size"] = size
	}
	if resolution := strings.TrimSpace(req.Resolution); resolution != "" {
		data["resolution"] = strings.ToLower(resolution)
	}
	data["effective_resolution"] = req.EffectiveResolutionTier()
	if ratio := dto.GeminiFlashImageResolutionPriceRatio(req.Resolution); strings.Contains(strings.ToLower(strings.TrimSpace(req.Model)), "flash-image") && ratio != 1.0 {
		data["resolution_price_ratio"] = ratio
	}
	if quality := strings.TrimSpace(req.Quality); quality != "" {
		data["quality"] = quality
	}
	if urls := imageURLsForLog(req.ImageUrls); len(urls) > 0 {
		data["image_urls"] = urls
	}
	return data
}

func imageURLsForLog(urls []string) []string {
	if len(urls) == 0 {
		return nil
	}
	filtered := make([]string, 0, len(urls))
	for _, raw := range urls {
		u := strings.TrimSpace(raw)
		if u == "" || strings.HasPrefix(strings.ToLower(u), "data:image") {
			continue
		}
		filtered = append(filtered, u)
	}
	return filtered
}
