package middleware

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestSelectedChannelHistoryPolicyAndInternalProbe(t *testing.T) {
	for _, tc := range []struct {
		name, body    string
		probe, reject bool
	}{
		{name: "incompatible direct fallback", body: `{"messages":[{"role":"tool","tool_call_id":"call_test"}]}`, reject: true},
		{name: "plain OpenAI request", body: `{"messages":[{"role":"user","content":"hello"}]}`},
		{name: "internal probe builds body after metadata", probe: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(tc.body))
			c.Request.Header.Set("Content-Type", "application/json")
			if tc.probe {
				c.Request.Body = nil
			}
			ch := &model.Channel{Id: 293, Type: 14, Name: "history-test", Status: 1, Key: "test", Setting: common.GetPointer(`{"anthropic_tool_history_models":["claude-sonnet-5-5"]}`)}
			err := SetupContextForSelectedChannel(c, ch, "claude-sonnet-5-5")
			if tc.reject {
				require.NotNil(t, err)
				require.Equal(t, 403, err.StatusCode)
			} else {
				require.Nil(t, err)
			}
		})
	}
}
