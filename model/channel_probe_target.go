package model

import (
	"fmt"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/types"
)

// A missing target is a legacy record. Invalid metadata must not fall back to
// an unrelated successful probe and silently re-enable the model.
func (channel *Channel) AutoDisabledModelProbeTargets(modelName string) ([]types.ChannelProbeTarget, error) {
	entries, _ := channel.GetOtherInfo()["auto_disabled_models"].(map[string]interface{})
	entry, _ := entries[modelName].(map[string]interface{})
	raw, exists := entry["probe_targets"]
	if !exists {
		return nil, nil
	}
	b, err := common.Marshal(raw)
	if err != nil {
		return nil, err
	}
	var targets []types.ChannelProbeTarget
	if err := common.Unmarshal(b, &targets); err != nil {
		return nil, err
	}
	if len(targets) == 0 {
		return nil, fmt.Errorf("empty saved probe targets for model %s", modelName)
	}
	for _, target := range targets {
		if !target.Valid() || target.ModelName != modelName {
			return nil, fmt.Errorf("invalid saved probe target for model %s", modelName)
		}
	}
	return targets, nil
}
