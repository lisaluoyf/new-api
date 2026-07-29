package service

import (
	"net/url"
	"strings"
)

const packyPricingRoot = "https://www.packyapi.com"

// ResolvePricingRoot returns the website root that exposes /api/pricing for a
// channel endpoint. API-only load-balancer hosts may not serve the management
// API even when the supplier's website does.
func ResolvePricingRoot(baseURL string) string {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")

	parsed, err := url.Parse(baseURL)
	if err == nil && strings.EqualFold(parsed.Hostname(), "slb-v1.api.fan") {
		return packyPricingRoot
	}

	for _, suffix := range []string{"/api/v1", "/openai/v1", "/v1", "/v2", "/v3"} {
		if strings.HasSuffix(baseURL, suffix) {
			return strings.TrimSuffix(baseURL, suffix)
		}
	}
	return baseURL
}
