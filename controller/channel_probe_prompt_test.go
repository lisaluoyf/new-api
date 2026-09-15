package controller

import (
	"fmt"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
)

func TestChannel82CodeReviewTestRequests(t *testing.T) {
	for _, channelID := range []int{82, 94} {
		for _, endpoint := range []string{"", string(constant.EndpointTypeOpenAI), string(constant.EndpointTypeOpenAIResponse)} {
			for _, stream := range []bool{false, true} {
				t.Run(fmt.Sprintf("%d/%s/stream=%t", channelID, endpoint, stream), func(t *testing.T) {
					request := buildTestRequest("gpt-6-astra", endpoint, &model.Channel{Id: channelID, Type: constant.ChannelTypeOpenAI}, stream)
					var prompt string
					switch req := request.(type) {
					case *dto.GeneralOpenAIRequest:
						prompt = req.Messages[0].Content.(string)
						require.Equal(t, stream, *req.Stream)
						if channelID == 82 {
							require.Equal(t, uint(1024), *req.MaxTokens)
						} else {
							require.Equal(t, uint(16), *req.MaxTokens)
						}
					case *dto.OpenAIResponsesRequest:
						var messages []dto.Message
						require.NoError(t, common.Unmarshal(req.Input, &messages))
						prompt = messages[0].Content.(string)
						require.Equal(t, stream, *req.Stream)
						if channelID == 82 {
							require.Equal(t, uint(1024), *req.MaxOutputTokens)
						} else {
							require.Nil(t, req.MaxOutputTokens)
						}
					default:
						t.Fatalf("unexpected request type %T", request)
					}
					if channelID == 82 {
						require.Contains(t, prompt, "def average(values)")
						require.Contains(t, prompt, "average([2, 4, 6])")
					} else {
						require.Equal(t, "hi", prompt)
					}
				})
			}
		}
	}
}

func TestChannel82CodeReviewLeavesNonChatRequestsUnchanged(t *testing.T) {
	channel := &model.Channel{Id: 82, Type: constant.ChannelTypeOpenAI}
	for _, endpoint := range []constant.EndpointType{constant.EndpointTypeEmbeddings, constant.EndpointTypeImageGeneration, constant.EndpointTypeJinaRerank, constant.EndpointTypeOpenAIResponseCompact} {
		require.Equal(t,
			buildDefaultTestRequest("test-model", string(endpoint), channel, false),
			buildTestRequest("test-model", string(endpoint), channel, false),
		)
	}
}
