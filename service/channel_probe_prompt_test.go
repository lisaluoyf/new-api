package service

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
)

func TestChannelCodeReviewProbeScope(t *testing.T) {
	for _, channel := range []*model.Channel{nil, {Id: 94, Type: constant.ChannelTypeOpenAI}, {Id: 82, Type: constant.ChannelTypeAnthropic}} {
		_, _, ok := ChannelCodeReviewProbe(channel)
		require.False(t, ok)
	}
}

func TestChannel82UptimeProbeBypassesTinyProbeGreeting(t *testing.T) {
	for _, channelID := range []int{82, 94} {
		t.Run(fmt.Sprint(channelID), func(t *testing.T) {
			var prompt string
			var limit uint
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body struct {
					Messages  []struct{ Content string } `json:"messages"`
					MaxTokens uint                       `json:"max_tokens"`
				}
				if err := common.DecodeJson(r.Body, &body); err != nil || len(body.Messages) != 1 {
					http.Error(w, "invalid request", http.StatusBadRequest)
					return
				}
				prompt, limit = body.Messages[0].Content, body.MaxTokens
				if !strings.Contains(prompt, "def average(values)") {
					w.Header().Set("Content-Type", "text/plain")
					fmt.Fprint(w, "Hi! What can I help you with?")
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Empty input divides by zero.\"}}]}\n\n")
			}))
			defer server.Close()
			_, err := sendUptimeProbe(context.Background(), server.Client(), server.URL, "test-key", "gpt-6-astra", &model.Channel{Id: channelID, Type: constant.ChannelTypeOpenAI})
			if channelID == 82 {
				require.Nil(t, err)
				require.Equal(t, uint(1024), limit)
			} else {
				require.NotNil(t, err)
				require.Equal(t, "hi", prompt)
				require.Equal(t, uint(16), limit)
			}
		})
	}
}
