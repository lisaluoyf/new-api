package service

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseFreeModelRequirementsAcrossEndpoints(t *testing.T) {
	tests := []struct {
		name                                  string
		path                                  string
		body                                  string
		endpoint                              FreeModelEndpoint
		vision, tools, object, schema, forced bool
	}{
		{"chat text", "/v1/chat/completions", `{"model":"apimaster-freemodel","messages":[{"role":"user","content":"hello"}]}`, FreeModelEndpointChatCompletions, false, false, false, false, false},
		{"chat vision tools", "/v1/chat/completions", `{"messages":[{"role":"user","content":[{"type":"text","text":"see"},{"type":"image_url","image_url":{"url":"https://example.test/a.png"}}]}],"tools":[{"type":"function","function":{"name":"x"}}],"tool_choice":"required"}`, FreeModelEndpointChatCompletions, true, true, false, false, true},
		{"responses schema", "/v1/responses", `{"input":[{"role":"user","content":[{"type":"input_image","image_url":"x"}]}],"text":{"format":{"type":"json_schema","name":"x","schema":{"type":"object"}}},"max_output_tokens":200}`, FreeModelEndpointResponses, true, false, true, true, false},
		{"messages json", "/v1/messages", `{"messages":[{"role":"user","content":"x"}],"tools":[{"name":"x"}],"tool_choice":{"type":"any"},"output_config":{"format":{"type":"json_object"}},"max_tokens":100}`, FreeModelEndpointMessages, false, true, true, false, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req, err := ParseFreeModelRequirements(test.path, []byte(test.body))
			require.NoError(t, err)
			require.Equal(t, test.endpoint, req.Endpoint)
			require.Equal(t, test.vision, req.Vision)
			require.Equal(t, test.tools, req.Tools)
			require.Equal(t, test.object, req.JSONObject)
			require.Equal(t, test.schema, req.JSONSchema)
			require.Equal(t, test.forced, req.RequiredToolCall)
			require.Positive(t, req.EstimatedInput)
		})
	}
}

func TestParseFreeModelRequirementsDoesNotCountBase64ImageAsText(t *testing.T) {
	image := strings.Repeat("A", 100000)
	body := `{"messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,` + image + `"}}]}],"max_tokens":100}`
	requirements, err := ParseFreeModelRequirements("/v1/chat/completions", []byte(body))
	require.NoError(t, err)
	require.True(t, requirements.Vision)
	require.Less(t, requirements.EstimatedInput, 1000)
}
