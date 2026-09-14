package controller

import (
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
)

func relayProbeTarget(info *relaycommon.RelayInfo) types.ChannelProbeTarget {
	if info == nil {
		return types.ChannelProbeTarget{}
	}
	target := types.ChannelProbeTarget{ModelName: info.OriginModelName, IsStream: info.IsStream}
	switch info.RelayFormat {
	case types.RelayFormatOpenAI:
		target.EndpointType = constant.EndpointTypeOpenAI
	case types.RelayFormatOpenAIResponses:
		target.EndpointType = constant.EndpointTypeOpenAIResponse
	case types.RelayFormatOpenAIResponsesCompaction:
		target.EndpointType = constant.EndpointTypeOpenAIResponseCompact
	case types.RelayFormatClaude:
		target.EndpointType = constant.EndpointTypeAnthropic
	case types.RelayFormatGemini:
		target.EndpointType = constant.EndpointTypeGemini
	case types.RelayFormatEmbedding:
		target.EndpointType = constant.EndpointTypeEmbeddings
	case types.RelayFormatRerank:
		target.EndpointType = constant.EndpointTypeJinaRerank
	case types.RelayFormatOpenAIImage:
		target.EndpointType = constant.EndpointTypeImageGeneration
	}
	return target
}

func probeAutoDisabledModel(channel *model.Channel, modelName string) (testResult, int64) {
	targets, err := channel.AutoDisabledModelProbeTargets(modelName)
	if err != nil {
		return testResult{localErr: err}, 0
	}
	return probeChannelForAutomation(channel, modelName, targets...)
}
