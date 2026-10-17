package service

import (
	"fmt"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

// TokenHub material ingestion defaults to Skip. Generation requests still
// validate explicit policies; Active never proves a strategy took effect.
type SeedanceModeration struct {
	Strategy string `json:"Strategy"`
}

func ParseSeedanceModeration(value any) (*SeedanceModeration, error) {
	raw, err := common.Marshal(value)
	var policy SeedanceModeration
	if err != nil || common.Unmarshal(raw, &policy) != nil || policy.Strategy != "Skip" {
		return nil, fmt.Errorf("Moderation must be an object with Strategy=Skip")
	}
	return &policy, nil
}

func seedanceModerationData(policy *SeedanceModeration, forwarded bool) string {
	if policy == nil {
		return ""
	}
	raw, _ := common.Marshal(map[string]any{"moderation": policy, "moderation_forwarded": forwarded})
	return string(raw)
}

func seedanceModerationDTO(asset *model.SeedanceResource) map[string]any {
	var saved struct {
		Moderation *SeedanceModeration `json:"moderation"`
		Forwarded  bool                `json:"moderation_forwarded"`
	}
	if common.UnmarshalJsonStr(asset.RequestData, &saved) != nil || saved.Moderation == nil {
		return nil
	}
	return map[string]any{"requested": saved.Moderation, "forwarded": saved.Forwarded, "provider_effect_confirmed": false}
}
