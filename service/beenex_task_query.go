package service

import (
	"fmt"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
)

// Defaults retain the deployed protocol; an explicit per-model setting takes priority.
func BeeNexTaskQueryProtocol(ch *model.Channel, name string) (string, error) {
	if ch == nil || ch.Id != constant.BeeNexSeedanceChannelID || !IsBeeNexSeedanceModel(name) {
		return "", fmt.Errorf("unsupported BeeNex query context")
	}
	protocol := ch.GetSetting().BeeNexQueryProtocols[name]
	if protocol == "" {
		protocol = "video_generations"
		if name == "seedance-2.5" {
			protocol = "sora"
		}
	}
	if protocol != "sora" && protocol != "video_generations" {
		return "", fmt.Errorf("invalid BeeNex task query protocol configuration")
	}
	return protocol, nil
}
