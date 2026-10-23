package klingv4

// OfficialPreviewPrices returns the Kling developer pricing page's Balance
// prices, USD per billable second, checked 2026-10-23. The separate Unit
// display must not be converted using the legacy V3 $0.14/unit rate.
func OfficialPreviewPrices() map[string]float64 {
	return map[string]float64{"720P": .157, "1080P": .235, "720P-input": .094, "1080P-input": .141}
}
