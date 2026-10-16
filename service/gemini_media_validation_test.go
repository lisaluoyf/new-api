package service

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/types"
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
)

func TestMarkdownDataImageValidation(t *testing.T) {
	const image = "![图](data:image/png;base64,aGVsbG8=)"
	parts, err := ParseMarkdownDataImages("前文" + image + "中间" + image + "后文（不能丢失）")
	require.NoError(t, err)
	require.Equal(t, []MarkdownDataImagePart{{Text: "前文"}, {MIME: "image/png", Data: "aGVsbG8="}, {Text: "中间"}, {MIME: "image/png", Data: "aGVsbG8="}, {Text: "后文（不能丢失）"}}, parts)
	for _, text := range []string{"plain text", "![remote](https://example.com/img.png) after", strings.Repeat("![", 10000) + "literal"} {
		parts, err := ParseMarkdownDataImages(text)
		require.NoError(t, err)
		require.Equal(t, []MarkdownDataImagePart{{Text: text}}, parts)
	}
	for _, text := range []string{
		"![x](data:image/png;base64,aGVsbG8", "![x](data:image/png;base64,aGVsbG8)",
		"![x](data:image/png;base64,aGVs\nprivate prose (later close))",
		"![x](data:image/png;base64,)", "![x](data:image/png;base64,INVALID!)",
		"![x](data:image/png,hello)", "![x](data:;base64,aGVsbG8=)",
	} {
		_, err := ParseMarkdownDataImages(text)
		require.Error(t, err, text)
		require.NotContains(t, err.Error(), "private prose")
	}
	// A malformed preceding label must not hide the next data image.
	parts, err = ParseMarkdownDataImages("![unfinished " + image + " tail")
	require.NoError(t, err)
	require.Len(t, parts, 3)
	require.Equal(t, " tail", parts[2].Text)
}

func TestGeminiMediaRequestForms(t *testing.T) {
	for _, content := range []string{
		`"![x](data:image/png;base64,broken!)"`,
		`[{"type":"text","text":"![x](data:image/png;base64,broken!)"}]`,
		`[{"type":"image_url","image_url":{"url":"data:image/png;base64,broken!"}}]`,
	} {
		var request dto.GeneralOpenAIRequest
		require.NoError(t, common.Unmarshal([]byte(`{"messages":[{"role":"user","content":`+content+`}]}`), &request))
		require.ErrorContains(t, ValidateGeminiRequestMedia(&request), "messages[0].content[0]")
	}
	for _, content := range []string{`"plain text"`, `[{"type":"image_url","image_url":{"url":"https://example.com/image.png"}}]`, `[{"type":"image_url","image_url":{"url":"data:image/png;base64,aGVsbG8="}}]`} {
		var request dto.GeneralOpenAIRequest
		require.NoError(t, common.Unmarshal([]byte(`{"messages":[{"role":"user","content":`+content+`}]}`), &request))
		require.NoError(t, ValidateGeminiRequestMedia(&request))
	}
	var native dto.GeminiChatRequest
	require.NoError(t, common.Unmarshal([]byte(`{"contents":[{"parts":[{"inline_data":{"mime_type":"image/png","data":"bad!"}}]}]}`), &native))
	require.ErrorContains(t, ValidateGeminiNativeMedia(&native), "contents[0].parts[0]")
}

func TestModelQuotaCooldownClassificationAndHealth(t *testing.T) {
	old := common.AutomaticDisableChannelEnabled
	common.AutomaticDisableChannelEnabled = true
	t.Cleanup(func() { common.AutomaticDisableChannelEnabled = old })
	for _, tc := range []struct {
		status        int
		code, message string
		cooldown      bool
	}{
		{429, "model_cooldown", "All credentials for model gemini-4-argon-high are cooling down", true},
		{503, "server_error", "auth_unavailable: no auth available; last upstream error 429 Individual quota reached. RESOURCE_EXHAUSTED", true},
		{429, "server_error", "Individual quota reached. RESOURCE_EXHAUSTED", true},
		{503, "server_error", "auth_unavailable: no auth available", false},
		{429, "rate_limit_error", "concurrency limit exceeded", false},
		{429, "insufficient_quota", "exceeded your current quota", false},
		{400, "model_cooldown", "All credentials are cooling down", false},
	} {
		e := types.WithOpenAIError(types.OpenAIError{Type: "server_error", Code: tc.code, Message: tc.message}, tc.status)
		require.Equal(t, tc.cooldown, isModelQuotaCooldown(e), tc.message)
		if tc.cooldown {
			require.Equal(t, CategoryModelQuotaCooldown, ClassifyChannelError(e))
			action, _ := EvaluateChannelHealth(types.ChannelError{ChannelId: 65, AutoBan: true}, e)
			require.Equal(t, HealthProbeBeforeDisable, action)
			action, _ = EvaluateChannelHealth(types.ChannelError{ChannelId: 65, AutoBan: false}, e)
			require.Equal(t, HealthSkip, action)
		}
	}
}
