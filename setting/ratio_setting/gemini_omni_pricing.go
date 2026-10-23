package ratio_setting

import (
	"fmt"
	"github.com/QuantumNous/new-api/common"
	"math"
	"strings"
)

const GeminiOmniPricingOption = "GeminiOmniPricing"

// APIMart golden procurement tariffs, USD per output second. Ext fixed-duration
// task prices are stored as effective per-second prices; reference includes input.
const defaultGeminiOmniPricing = `{"Official-360P":0.0296,"Ext-360P-4s":0.0375,"Ext-360P-6s":0.029166666666666664,"Ext-360P-8s":0.025,"Ext-360P-10s":0.0225,"Ext-360P-reference":0.04,"Official-720P":0.088,"Ext-720P-4s":0.0625,"Ext-720P-6s":0.049999999999999996,"Ext-720P-8s":0.04375,"Ext-720P-10s":0.04,"Ext-720P-reference":0.08,"Official-1080P":0.132,"Ext-1080P-4s":0.0625,"Ext-1080P-6s":0.049999999999999996,"Ext-1080P-8s":0.04375,"Ext-1080P-10s":0.04,"Ext-1080P-reference":0.08,"Official-4K":0.264,"Ext-4K-4s":0.1875,"Ext-4K-6s":0.13333333333333333,"Ext-4K-8s":0.10625,"Ext-4K-10s":0.09,"Ext-4K-reference":0.24}`

func DefaultGeminiOmniPricingJSON() string { return defaultGeminiOmniPricing }

func ValidateGeminiOmniPricing(raw string) error {
	var values, defaults map[string]float64
	if err := common.UnmarshalJsonStr(raw, &values); err != nil {
		return fmt.Errorf("invalid Gemini Omni pricing: %w", err)
	}
	_ = common.UnmarshalJsonStr(defaultGeminiOmniPricing, &defaults)
	if len(values) != len(defaults) {
		return fmt.Errorf("Gemini Omni requires all 24 route price tiers")
	}
	for key := range defaults {
		v := values[key]
		if v <= 0 || math.IsNaN(v) || math.IsInf(v, 0) {
			return fmt.Errorf("Gemini Omni price %s must be positive and finite", key)
		}
	}
	return nil
}

func GetGeminiOmniPrice(res string, seconds int, ext, video bool) float64 {
	key := "Official-" + strings.ToUpper(res)
	if ext {
		key = fmt.Sprintf("Ext-%s-%ds", strings.ToUpper(res), seconds)
		if video {
			key = "Ext-" + strings.ToUpper(res) + "-reference"
		}
	}
	common.OptionMapRWMutex.RLock()
	raw := common.OptionMap[GeminiOmniPricingOption]
	common.OptionMapRWMutex.RUnlock()
	var values map[string]float64
	if common.UnmarshalJsonStr(raw, &values) == nil && values[key] > 0 {
		return values[key] * float64(seconds)
	}
	_ = common.UnmarshalJsonStr(defaultGeminiOmniPricing, &values)
	return values[key] * float64(seconds)
}
