package ratio_setting

import (
	"fmt"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/types"
	"math"
	"strings"
)

var defaultCacheRatio = map[string]float64{
	"claude-opus-5-5":                     0.05,
	"claude-sonnet-5-5":                   0.05,
	"claude-sonnet-5.5":                   0.05,
	"qwen3.8-max":                         0.2,
	"qwen3.8-flash":                       0.1,
	"gemini-3-flash-preview":              0.1,
	"gemini-3-pro-preview":                0.1,
	"gemini-3.1-pro-preview":              0.1,
	"gemini-3.7-flash":                    0.1,
	"gemini-3.8-flash":                    0.1,
	"gemini-3.6-flash":                    0.1,
	"gemini-3.5-flash":                    0.1,
	"gemini-3.1-flash-lite":               0.1,
	"gpt-4":                               0.5,
	"o1":                                  0.5,
	"o1-2024-12-17":                       0.5,
	"o1-preview-2024-09-12":               0.5,
	"o1-preview":                          0.5,
	"o1-mini-2024-09-12":                  0.5,
	"o1-mini":                             0.5,
	"o3-mini":                             0.5,
	"o3-mini-2025-01-31":                  0.5,
	"gpt-4o-2024-11-20":                   0.5,
	"gpt-4o-2024-08-06":                   0.5,
	"gpt-4o":                              0.5,
	"gpt-4o-mini-2024-07-18":              0.5,
	"gpt-4o-mini":                         0.5,
	"gpt-4o-realtime-preview":             0.5,
	"gpt-4o-mini-realtime-preview":        0.5,
	"gpt-4.5-preview":                     0.5,
	"gpt-4.5-preview-2025-02-27":          0.5,
	"gpt-4.1":                             0.25,
	"gpt-4.1-mini":                        0.25,
	"gpt-4.1-nano":                        0.25,
	"gpt-5":                               0.1,
	"gpt-5-2025-08-07":                    0.1,
	"gpt-5-chat-latest":                   0.1,
	"gpt-5-mini":                          0.1,
	"gpt-5-mini-2025-08-07":               0.1,
	"gpt-5-nano":                          0.1,
	"gpt-5-nano-2025-08-07":               0.1,
	"gpt-6-astra":                         0.1,
	"deepseek-chat":                       0.25,
	"deepseek-reasoner":                   0.25,
	"deepseek-coder":                      0.25,
	"claude-3-sonnet-20240229":            0.1,
	"claude-3-opus-20240229":              0.1,
	"claude-3-haiku-20240307":             0.1,
	"claude-3-5-haiku-20241022":           0.1,
	"claude-haiku-4-5-20251001":           0.1,
	"claude-3-5-sonnet-20240620":          0.1,
	"claude-3-5-sonnet-20241022":          0.1,
	"claude-3-7-sonnet-20250219":          0.1,
	"claude-3-7-sonnet-20250219-thinking": 0.1,
	"claude-sonnet-4-20250514":            0.1,
	"claude-sonnet-4-20250514-thinking":   0.1,
	"claude-opus-4-20250514":              0.1,
	"claude-opus-4-20250514-thinking":     0.1,
	"claude-opus-4-1-20250805":            0.1,
	"claude-opus-4-1-20250805-thinking":   0.1,
	"claude-sonnet-4-5-20250929":          0.1,
	"claude-sonnet-4-5-20250929-thinking": 0.1,
	"claude-opus-4-5-20251101":            0.1,
	"claude-opus-4-5-20251101-thinking":   0.1,
	"claude-opus-4-6":                     0.1,
	"claude-opus-4-6-thinking":            0.1,
	"claude-opus-4-6-max":                 0.1,
	"claude-opus-4-6-high":                0.1,
	"claude-opus-4-6-medium":              0.1,
	"claude-opus-4-6-low":                 0.1,
	"claude-opus-4-7":                     0.1,
	"claude-opus-4-7-thinking":            0.1,
	"claude-opus-4-7-max":                 0.1,
	"claude-opus-4-7-xhigh":               0.1,
	"claude-opus-4-7-high":                0.1,
	"claude-opus-4-7-medium":              0.1,
	"claude-opus-4-7-low":                 0.1,
	"claude-opus-5":                       0.1,
}

var defaultCreateCacheRatio = map[string]float64{
	"claude-opus-5-5":                     1.25,
	"claude-sonnet-5-5":                   1.25,
	"claude-sonnet-5.5":                   1.25,
	"gpt-6-astra":                         1.25,
	"qwen3.8-max":                         1.25,
	"qwen3.8-flash":                       1.333333333333,
	"claude-3-sonnet-20240229":            1.25,
	"claude-3-opus-20240229":              1.25,
	"claude-3-haiku-20240307":             1.25,
	"claude-3-5-haiku-20241022":           1.25,
	"claude-haiku-4-5-20251001":           1.25,
	"claude-3-5-sonnet-20240620":          1.25,
	"claude-3-5-sonnet-20241022":          1.25,
	"claude-3-7-sonnet-20250219":          1.25,
	"claude-3-7-sonnet-20250219-thinking": 1.25,
	"claude-sonnet-4-20250514":            1.25,
	"claude-sonnet-4-20250514-thinking":   1.25,
	"claude-opus-4-20250514":              1.25,
	"claude-opus-4-20250514-thinking":     1.25,
	"claude-opus-4-1-20250805":            1.25,
	"claude-opus-4-1-20250805-thinking":   1.25,
	"claude-sonnet-4-5-20250929":          1.25,
	"claude-sonnet-4-5-20250929-thinking": 1.25,
	"claude-opus-4-5-20251101":            1.25,
	"claude-opus-4-5-20251101-thinking":   1.25,
	"claude-opus-4-6":                     1.25,
	"claude-opus-4-6-thinking":            1.25,
	"claude-opus-4-6-max":                 1.25,
	"claude-opus-4-6-high":                1.25,
	"claude-opus-4-6-medium":              1.25,
	"claude-opus-4-6-low":                 1.25,
	"claude-opus-4-7":                     1.25,
	"claude-opus-4-7-thinking":            1.25,
	"claude-opus-4-7-max":                 1.25,
	"claude-opus-4-7-xhigh":               1.25,
	"claude-opus-4-7-high":                1.25,
	"claude-opus-4-7-medium":              1.25,
	"claude-opus-4-7-low":                 1.25,
	"claude-opus-5":                       1.25,
}

//var defaultCreateCacheRatio = map[string]float64{}

var cacheRatioMap = types.NewRWMap[string, float64]()
var createCacheRatioMap = types.NewRWMap[string, float64]()

// GetCacheRatioMap returns a copy of the cache ratio map
func GetCacheRatioMap() map[string]float64 {
	return cacheRatioMap.ReadAll()
}

// CacheRatio2JSONString converts the cache ratio map to a JSON string
func CacheRatio2JSONString() string {
	return cacheRatioMap.MarshalJSONString()
}

// CreateCacheRatio2JSONString converts the create cache ratio map to a JSON string
func CreateCacheRatio2JSONString() string {
	return createCacheRatioMap.MarshalJSONString()
}

// UpdateCacheRatioByJSONString updates the cache ratio map from a JSON string
func UpdateCacheRatioByJSONString(jsonStr string) error {
	return types.LoadFromJsonStringWithCallback(cacheRatioMap, jsonStr, InvalidateExposedDataCache)
}

// UpdateCreateCacheRatioByJSONString updates the create cache ratio map from a JSON string
func UpdateCreateCacheRatioByJSONString(jsonStr string) error {
	return types.LoadFromJsonStringWithCallback(createCacheRatioMap, jsonStr, InvalidateExposedDataCache)
}

// GetCacheRatio returns the cache ratio for a model
func GetCacheRatio(name string) (float64, bool) {
	ratio, ok := cacheRatioMap.Get(name)
	if !ok {
		return 1, false // Default to 1 if not found
	}
	return ratio, true
}

func GetCreateCacheRatio(name string) (float64, bool) {
	ratio, ok := createCacheRatioMap.Get(name)
	if !ok {
		return 1.25, false // Default to 1.25 if not found
	}
	return ratio, true
}

func GetCacheRatioCopy() map[string]float64 {
	return cacheRatioMap.ReadAll()
}

func GetCreateCacheRatioCopy() map[string]float64 {
	return createCacheRatioMap.ReadAll()
}

// Separate 1h overrides preserve explicit zero; missing Claude overrides retain
// the historical 2x-input / 1.25x-input TTL ratio used by settlement.
var createCache1hRatioMap = types.NewRWMap[string, float64]()

func CreateCache1hRatio2JSONString() string         { return createCache1hRatioMap.MarshalJSONString() }
func GetCreateCache1hRatioCopy() map[string]float64 { return createCache1hRatioMap.ReadAll() }
func UpdateCreateCache1hRatioByJSONString(raw string) error {
	var values map[string]float64
	if err := common.Unmarshal([]byte(raw), &values); err != nil {
		return err
	}
	for _, value := range values {
		if value < 0 || math.IsNaN(value) || math.IsInf(value, 0) {
			return fmt.Errorf("1h cache ratio must be finite and non-negative")
		}
	}
	return types.LoadFromJsonStringWithCallback(createCache1hRatioMap, raw, InvalidateExposedDataCache)
}
func GetCreateCache1hRatio(name string) (float64, bool) {
	if value, ok := createCache1hRatioMap.Get(name); ok {
		return value, true
	}
	if !strings.Contains(strings.ToLower(name), "claude") {
		return 0, false
	}
	base, _ := GetCreateCacheRatio(name)
	return base * 1.6, true
}

// ResolveCacheCreation1hPrice is also used for older channel catalogs which
// exported only the 5m base. Explicit channel overrides always take precedence.
func ResolveCacheCreation1hPrice(name string, base float64, explicit *float64) *float64 {
	if explicit != nil {
		return explicit
	}
	ratio, ok := GetCreateCache1hRatio(name)
	if !ok {
		return nil
	}
	fiveMinuteRatio, _ := GetCreateCacheRatio(name)
	if fiveMinuteRatio <= 0 {
		return nil
	}
	value := base * ratio / fiveMinuteRatio
	return &value
}

// Preserve channel-specific 5m ratios instead of substituting the global base.
func CacheCreation1hRatioFromBase(name string, base float64) float64 {
	if price := ResolveCacheCreation1hPrice(name, base, nil); price != nil {
		return *price
	}
	return base * 1.6
}
