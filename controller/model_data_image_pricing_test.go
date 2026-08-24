package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/stretchr/testify/require"
)

func TestBuildImagePricingViewAppliesAllChannelCoefficients(t *testing.T) {
	common.OptionMapRWMutex.Lock()
	if common.OptionMap == nil {
		common.OptionMap = make(map[string]string)
	}
	previous, hadPrevious := common.OptionMap[ratio_setting.ImageModelPricingOption]
	common.OptionMap[ratio_setting.ImageModelPricingOption] = ratio_setting.DefaultImageModelPricingJSON()
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		defer common.OptionMapRWMutex.Unlock()
		if hadPrevious {
			common.OptionMap[ratio_setting.ImageModelPricingOption] = previous
		} else {
			delete(common.OptionMap, ratio_setting.ImageModelPricingOption)
		}
	})

	view := buildImagePricingView("gpt-image-2", 0.05, 0.8, 3)
	require.NotNil(t, view)
	require.Equal(t, "image", view.Unit)
	require.Equal(t, "1K", view.BaseVariant)
	require.InDelta(t, 0.25, view.OfficialPrices["1K"], 1e-9)
	require.InDelta(t, 0.25*0.05*0.8, view.ProcurementPrices["1K"], 1e-9)
	require.InDelta(t, 0.25*0.05*0.8*3, view.BillingPrices["1K"], 1e-9)
	require.InDelta(t, 0.30*0.05*0.8*3, view.BillingPrices["2K"], 1e-9)
	require.InDelta(t, 0.60*0.05*0.8*3, view.BillingPrices["4K"], 1e-9)
}
