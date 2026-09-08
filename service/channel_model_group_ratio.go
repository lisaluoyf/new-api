package service

import (
	"math"
	"strings"

	"github.com/QuantumNous/new-api/common"
)

// Overrides use the client-facing model key, never a reverse model mapping:
// two public models can share an upstream pricing row without sharing policy.
func ModelGroupRatioOverride(setting *string, modelName string) (float64, bool) {
	if setting == nil || *setting == "" {
		return 0, false
	}
	var settings struct {
		ModelGroupRatios map[string]float64 `json:"model_group_ratios"`
	}
	if common.UnmarshalJsonStr(*setting, &settings) != nil {
		return 0, false
	}
	ratio := settings.ModelGroupRatios[strings.TrimSpace(modelName)]
	return ratio, ratio > 0 && !math.IsNaN(ratio) && !math.IsInf(ratio, 0)
}

func EffectiveManualGroupRatio(setting *string, modelName string) float64 {
	if ratio, ok := ModelGroupRatioOverride(setting, modelName); ok {
		return ratio
	}
	return ExtractManualGroupRatio(setting)
}

// Pricing snapshots retain the default/upstream multiplier. Apply the model
// override only when reading, so deleting it restores the original price even
// if the upstream is unavailable. Dividing out the stored ratio avoids stacking.
func ApplyModelGroupRatio(setting *string, modelName string, row *ChannelPricingLookupRow) {
	if row == nil || row.PricingSource == "free_model" || IsFreeModel(modelName) {
		return
	}
	ratio, ok := ModelGroupRatioOverride(setting, modelName)
	if !ok {
		return
	}
	previous := row.GroupRatio
	if previous <= 0 {
		previous = 1
	}
	factor := ratio / previous
	row.InputPrice *= factor
	row.OutputPrice *= factor
	row.CachePrice *= factor
	row.CacheCreationPrice *= factor
	row.GroupRatio = ratio
}
