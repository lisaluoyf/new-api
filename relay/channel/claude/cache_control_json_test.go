package claude

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestRequestOpenAI2ClaudeMessagePreservesCacheControlFromJSON(t *testing.T) {
	const raw = `{"model":"claude-opus-5","max_tokens":32,"messages":[
		{"role":"system","content":[{"type":"text","text":"stable system prefix","cache_control":{"type":"ephemeral","ttl":"1h"}}]},
		{"role":"user","content":[
			{"type":"text","text":"stable user prefix","cache_control":{"type":"ephemeral"}},
			{"type":"file","file":{"filename":"notes.txt","file_data":"aGVsbG8="},"cache_control":{"type":"ephemeral"}},
			{"type":"text","text":"uncached question"}
		]}
	]}`
	var request dto.GeneralOpenAIRequest
	require.NoError(t, common.DecodeJson(strings.NewReader(raw), &request))
	// Token estimation can parse content before the provider conversion.
	for i := range request.Messages {
		request.Messages[i].ParseContent()
	}
	converted, err := RequestOpenAI2ClaudeMessage(nil, request)
	require.NoError(t, err)
	encoded, err := common.Marshal(converted)
	require.NoError(t, err)
	require.JSONEq(t, `{"type":"ephemeral","ttl":"1h"}`, gjson.GetBytes(encoded, "system.0.cache_control").Raw)
	require.JSONEq(t, `{"type":"ephemeral"}`, gjson.GetBytes(encoded, "messages.0.content.0.cache_control").Raw)
	require.JSONEq(t, `{"type":"ephemeral"}`, gjson.GetBytes(encoded, "messages.0.content.1.cache_control").Raw)
	require.Equal(t, "hello", gjson.GetBytes(encoded, "messages.0.content.1.text").String())
	require.False(t, gjson.GetBytes(encoded, "messages.0.content.2.cache_control").Exists())
}

func TestRequestOpenAI2ClaudeMessagePreservesMediaCacheControlFromJSON(t *testing.T) {
	for _, tc := range []struct {
		name, block, wantType string
	}{
		{"image", `{"type":"image_url","image_url":{"url":"data:image/png;base64,aGVsbG8="},"cache_control":{"type":"ephemeral"}}`, "image"},
		{"pdf", `{"type":"file","file":{"filename":"notes.pdf","file_data":"aGVsbG8="},"cache_control":{"type":"ephemeral"}}`, "document"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var request dto.GeneralOpenAIRequest
			require.NoError(t, common.Unmarshal([]byte(`{"model":"claude-opus-5","messages":[{"role":"user","content":[`+tc.block+`]}]}`), &request))
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			converted, err := RequestOpenAI2ClaudeMessage(ctx, request)
			require.NoError(t, err)
			encoded, err := common.Marshal(converted)
			require.NoError(t, err)
			require.Equal(t, tc.wantType, gjson.GetBytes(encoded, "messages.0.content.0.type").String())
			require.JSONEq(t, `{"type":"ephemeral"}`, gjson.GetBytes(encoded, "messages.0.content.0.cache_control").Raw)
			require.Equal(t, "aGVsbG8=", gjson.GetBytes(encoded, "messages.0.content.0.source.data").String())
		})
	}
}
