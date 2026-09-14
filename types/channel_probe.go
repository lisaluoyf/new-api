package types

import (
	"fmt"

	"github.com/QuantumNous/new-api/constant"
)

// ChannelProbeTarget contains only routing metadata, never customer input or credentials.
// It is also the health-window key within a channel.
type ChannelProbeTarget struct {
	ModelName    string                `json:"model_name"`
	EndpointType constant.EndpointType `json:"endpoint_type"`
	IsStream     bool                  `json:"is_stream"`
}

func (t ChannelProbeTarget) Valid() bool {
	if t.ModelName == "" {
		return false
	}
	switch t.EndpointType {
	case constant.EndpointTypeOpenAI, constant.EndpointTypeOpenAIResponse,
		constant.EndpointTypeOpenAIResponseCompact, constant.EndpointTypeAnthropic,
		constant.EndpointTypeGemini, constant.EndpointTypeJinaRerank,
		constant.EndpointTypeImageGeneration, constant.EndpointTypeEmbeddings:
		return true
	}
	return false
}

func (t ChannelProbeTarget) String() string {
	return fmt.Sprintf("model=%s endpoint=%s stream=%t", t.ModelName, t.EndpointType, t.IsStream)
}
