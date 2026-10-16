package gemini

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"net/http/httptest"
	"testing"
)

func TestMarkdownImagesPreserveFollowingText(t *testing.T) {
	var req dto.GeneralOpenAIRequest
	require.NoError(t, common.Unmarshal([]byte(`{"model":"gemini-4-argon","messages":[{"role":"user","content":"before ![a](data:image/png;base64,aGVsbG8=) between ![b](data:image/png;base64,aGVsbG8=) after 中文正文"}]}`), &req))
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	info := &relaycommon.RelayInfo{OriginModelName: req.Model, ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeGemini, UpstreamModelName: req.Model}}
	converted, err := CovertOpenAI2Gemini(c, req, info)
	require.NoError(t, err)
	require.Len(t, converted.Contents, 1)
	parts := converted.Contents[0].Parts
	require.Len(t, parts, 5)
	require.Equal(t, "before ", parts[0].Text)
	require.NotNil(t, parts[1].InlineData)
	require.Equal(t, " between ", parts[2].Text)
	require.NotNil(t, parts[3].InlineData)
	require.Equal(t, " after 中文正文", parts[4].Text)
	req.Messages[0].Content = "![a](data:image/png;base64,broken!)"
	_, err = CovertOpenAI2Gemini(c, req, info)
	require.Error(t, err)
}
