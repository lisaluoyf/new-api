package model

import (
	"encoding/json"
	"strings"

	"github.com/QuantumNous/new-api/common"
)

// PreserveAnthropicRoutingSettings keeps routing capabilities omitted by older
// channel editors. Explicit false/[]/null values still clear a policy.
// Other fields retain the existing channel edit replacement semantics.
func (channel *Channel) PreserveAnthropicRoutingSettings(origin *Channel) error {
	if channel.Setting == nil || origin == nil || origin.Setting == nil || strings.TrimSpace(*origin.Setting) == "" {
		return nil
	}
	var before map[string]json.RawMessage
	if err := common.UnmarshalJsonStr(*origin.Setting, &before); err != nil {
		return err
	}
	after := map[string]json.RawMessage{}
	if strings.TrimSpace(*channel.Setting) != "" {
		if err := common.UnmarshalJsonStr(*channel.Setting, &after); err != nil {
			return err
		}
	}
	if after == nil {
		after = map[string]json.RawMessage{}
	}
	changed := false
	for _, key := range []string{"native_anthropic_only", "anthropic_tool_history_models"} {
		if _, present := after[key]; present {
			continue
		}
		if value, present := before[key]; present {
			after[key] = value
			changed = true
		}
	}
	if !changed {
		return nil
	}
	data, err := common.Marshal(after)
	if err != nil {
		return err
	}
	channel.Setting = common.GetPointer(string(data))
	return channel.ValidateSettings()
}
