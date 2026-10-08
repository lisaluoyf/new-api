package service

import (
	"fmt"
	"math"
	"strings"

	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/QuantumNous/new-api/types"
)

func ValidateUserModelDiscountRatios(source map[string]float64) error {
	seen := make(map[string]bool, len(source))
	for modelName, value := range source {
		modelName = strings.TrimSpace(modelName)
		if modelName == "" || seen[modelName] || math.IsNaN(value) || math.IsInf(value, 0) || value < minUserModelDiscount || value > maxUserModelDiscount {
			return fmt.Errorf("invalid model discount")
		}
		seen[modelName] = true
	}
	return nil
}

const (
	minUserModelDiscount = 0.000001
	maxUserModelDiscount = 1.0
)

func NormalizeUserModelDiscount(value float64) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) || value <= 0 || value > maxUserModelDiscount {
		return 1
	}
	if value < minUserModelDiscount {
		return minUserModelDiscount
	}
	return value
}

func SanitizeUserModelDiscountRatios(source map[string]float64) map[string]float64 {
	if len(source) == 0 {
		return nil
	}
	result := make(map[string]float64, len(source))
	for modelName, value := range source {
		modelName = strings.TrimSpace(modelName)
		if modelName == "" {
			continue
		}
		if math.IsNaN(value) || math.IsInf(value, 0) || value <= 0 || value > maxUserModelDiscount {
			continue
		}
		result[modelName] = NormalizeUserModelDiscount(value)
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func ResolveUserModelDiscount(setting dto.UserSetting, modelName string) float64 {
	if ratio, ok := setting.ModelDiscountRatios[strings.TrimSpace(modelName)]; ok {
		return NormalizeUserModelDiscount(ratio)
	}
	return 1
}

func ApplyUserModelDiscountToGroupRatio(info types.GroupRatioInfo, setting dto.UserSetting, modelName string) types.GroupRatioInfo {
	if info.UserModelDiscount > 0 {
		return info
	}
	discount := ResolveUserModelDiscount(setting, modelName)
	if discount != 1 {
		info.GroupRatio *= discount
		info.UserModelDiscount = discount
	}
	return info
}

func ApplyUserModelDiscountToPriceData(priceData types.PriceData, setting dto.UserSetting, modelName string) types.PriceData {
	if priceData.FreeModel || priceData.GroupRatioInfo.UserModelDiscount > 0 {
		return priceData
	}
	discount := ResolveUserModelDiscount(setting, modelName)
	if discount == 1 {
		return priceData
	}
	priceData.GroupRatioInfo = ApplyUserModelDiscountToGroupRatio(priceData.GroupRatioInfo, setting, modelName)
	priceData.Quota = int(float64(priceData.Quota) * discount)
	priceData.QuotaToPreConsume = int(float64(priceData.QuotaToPreConsume) * discount)
	return priceData
}

func ApplyUserModelDiscountToQuota(quota int, setting dto.UserSetting, modelName string) int {
	discount := ResolveUserModelDiscount(setting, modelName)
	if discount == 1 {
		return quota
	}
	return int(float64(quota) * discount)
}

func ApplyUserModelDiscountToBillingSnapshot(snapshot *billingexpr.BillingSnapshot, setting dto.UserSetting, modelName string) *billingexpr.BillingSnapshot {
	discount := ResolveUserModelDiscount(setting, modelName)
	if snapshot == nil || discount == 1 {
		return snapshot
	}
	copy := *snapshot
	copy.GroupRatio *= discount
	copy.EstimatedQuotaAfterGroup = ApplyUserModelDiscountToQuota(snapshot.EstimatedQuotaAfterGroup, setting, modelName)
	return &copy
}

func UserModelDiscountLogGroupRatio(info types.GroupRatioInfo) float64 {
	if info.UserModelDiscount > 0 && info.UserModelDiscount < 1 {
		return info.GroupRatio / info.UserModelDiscount
	}
	return info.GroupRatio
}
