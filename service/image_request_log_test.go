package service

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/stretchr/testify/require"
)

func TestApplyImageBillingLogInfoUsesPerImageOfficialPrice(t *testing.T) {
	oldOptions := common.OptionMap
	t.Cleanup(func() { common.OptionMap = oldOptions })
	common.OptionMap = map[string]string{ratio_setting.ImageModelPricingOption: ratio_setting.DefaultImageModelPricingJSON()}

	other := map[string]interface{}{}
	ok := ApplyImageBillingLogInfo(other, "gemini-3.1-flash-image", map[string]interface{}{
		"actual_image_count":   2,
		"effective_resolution": "2K",
	})

	require.True(t, ok)
	require.Equal(t, accountingBillingModeImageCount, other["billing_mode"])
	require.InDelta(t, 0.101, other["model_price"], 0.000001)
	billing := other["image_billing"].(map[string]interface{})
	require.Equal(t, 2, billing["generated_images"])
	require.InDelta(t, 0.202, billing["base_amount_usd"], 0.000001)
}

func TestBuildImageRequestDataForLog(t *testing.T) {
	t.Parallel()
	n := uint(1)
	req := &dto.ImageRequest{
		Model:      "gemini-3.1-flash-image-preview",
		Prompt:     "太阳在西边",
		N:          &n,
		Size:       "16:9",
		Resolution: "2k",
	}
	data := BuildImageRequestDataForLog(req)
	require.Equal(t, "gemini-3.1-flash-image-preview", data["model"])
	require.Equal(t, "16:9", data["size"])
	require.Equal(t, "2k", data["resolution"])
	require.Equal(t, "2K", data["effective_resolution"])
	require.InDelta(t, 4.0/3.0, data["resolution_price_ratio"].(float64), 0.001)
}

func TestBuildImageRequestDataForLogKeepsOnlyImageURLs(t *testing.T) {
	t.Parallel()
	req := &dto.ImageRequest{
		Model:  "gpt-image-2",
		Prompt: "edit this",
		ImageUrls: []string{
			"data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAA",
			" https://example.com/input.png ",
			"",
			"data:image/jpeg;base64,/9j/4AAQSkZJRg",
			"https://cdn.example.com/ref.webp",
		},
	}

	data := BuildImageRequestDataForLog(req)

	require.Equal(t, []string{
		"https://example.com/input.png",
		"https://cdn.example.com/ref.webp",
	}, data["image_urls"])
}

func TestBuildImageRequestDataForLogOmitsOnlyInlineImages(t *testing.T) {
	t.Parallel()
	req := &dto.ImageRequest{
		Model:  "gpt-image-2",
		Prompt: "edit this",
		ImageUrls: []string{
			"data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAA",
			"data:image/jpeg;base64,/9j/4AAQSkZJRg",
		},
	}

	data := BuildImageRequestDataForLog(req)

	require.NotContains(t, data, "image_urls")
}
