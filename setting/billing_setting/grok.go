package billing_setting

import "strings"

// xAI Standard API pricing, verified 2026-10-17:
// https://docs.x.ai/developers/pricing
// The long-context rates apply to the entire request at >= 200,000 prompt
// tokens, including cached input. Output length never selects the tier.
const (
	Grok45BillingExpr = `len < 200000 ? tier("short_context", p * 2 + cr * 0.3 + c * 6) : tier("long_context", p * 4 + cr * 0.6 + c * 12)`
	Grok46BillingExpr = `len < 200000 ? tier("short_context", p * 2 + cr * 0.5 + c * 6) : tier("long_context", p * 4 + cr * 1 + c * 12)`
	Grok47BillingExpr = Grok46BillingExpr
)

// Only these published model IDs share this schedule. In particular, do not
// apply it to Grok image/video, older Fast models, or unverified variants.
func GrokOfficialBillingExpr(model string) (string, bool) {
	switch strings.TrimPrefix(strings.TrimSpace(model), "x-ai/") {
	case "grok-4.5":
		return Grok45BillingExpr, true
	case "grok-4.6":
		return Grok46BillingExpr, true
	case "grok-4.7":
		return Grok47BillingExpr, true
	}
	return "", false
}

// GrokOfficialBasePrices anchors channel discounts to the short-context tier.
func GrokOfficialBasePrices(model string) (input, output, cache, cacheWrite float64, ok bool) {
	expression, ok := GrokOfficialBillingExpr(model)
	if !ok {
		return 0, 0, 0, 0, false
	}
	cache = .5
	if expression == Grok45BillingExpr {
		cache = .3
	}
	return 2, 6, cache, 0, true
}
