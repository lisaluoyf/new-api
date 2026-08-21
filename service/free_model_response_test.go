package service

import (
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func freeResponseContext(req FreeModelRequirements) *gin.Context {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set("original_model", FreeModelID)
	SetFreeModelCandidatePlan(c, &FreeModelCandidatePlan{Requirements: req})
	return c
}

func responseWith(content, finish, arguments string) *dto.OpenAITextResponse {
	message := dto.Message{Content: content}
	if arguments != "" {
		raw, _ := common.Marshal([]dto.ToolCallResponse{{Function: dto.FunctionResponse{Name: "x", Arguments: arguments}}})
		message.ToolCalls = raw
	}
	return &dto.OpenAITextResponse{Choices: []dto.OpenAITextResponseChoice{{Message: message, FinishReason: finish}}}
}

func TestValidateFreeModelResponseJSONSchema(t *testing.T) {
	req := FreeModelRequirements{Text: true, JSONObject: true, JSONSchema: true, Schema: map[string]any{"type": "object", "required": []any{"name"}, "properties": map[string]any{"name": map[string]any{"type": "string"}}, "additionalProperties": false}}
	require.Nil(t, ValidateFreeModelOpenAIResponse(freeResponseContext(req), responseWith(`{"name":"ok"}`, "stop", "")))
	err := ValidateFreeModelOpenAIResponse(freeResponseContext(req), responseWith(`{"wrong":1}`, "stop", ""))
	require.Equal(t, "schema_validation_failed", string(err.GetErrorCode()))
}

func TestValidateFreeModelResponseToolArgumentsAndEmpty(t *testing.T) {
	err := ValidateFreeModelOpenAIResponse(freeResponseContext(FreeModelRequirements{Text: true, Tools: true}), responseWith("", "tool_calls", `{broken`))
	require.Equal(t, "invalid_tool_arguments", string(err.GetErrorCode()))
	err = ValidateFreeModelOpenAIResponse(freeResponseContext(FreeModelRequirements{Text: true}), responseWith("", "stop", ""))
	require.Equal(t, "empty_response", string(err.GetErrorCode()))
	err = ValidateFreeModelOpenAIResponse(freeResponseContext(FreeModelRequirements{Text: true, Tools: true, RequiredToolCall: true}), responseWith("not a tool", "stop", ""))
	require.Equal(t, "missing_required_tool_call", string(err.GetErrorCode()))
}

func TestValidateFreeModelResponseContentFilterIsTerminal(t *testing.T) {
	require.Nil(t, ValidateFreeModelOpenAIResponse(
		freeResponseContext(FreeModelRequirements{Text: true}),
		responseWith("", "content_filter", ""),
	))
}

func TestValidateFreeModelResponsesResponseSchemaFailure(t *testing.T) {
	req := FreeModelRequirements{Text: true, JSONObject: true, JSONSchema: true, Schema: map[string]any{
		"type": "object", "required": []any{"answer"}, "properties": map[string]any{"answer": map[string]any{"type": "integer"}},
	}}
	response := &dto.OpenAIResponsesResponse{Output: []dto.ResponsesOutput{{
		Type: "message", Content: []dto.ResponsesOutputContent{{Type: "output_text", Text: `{"answer":"wrong"}`}},
	}}}
	err := ValidateFreeModelResponsesResponse(freeResponseContext(req), response)
	require.Equal(t, "schema_validation_failed", string(err.GetErrorCode()))
}

func TestFreeModelMemberExplicitFalsePersists(t *testing.T) {
	db := setupFreeModelRouterDB(t)
	cfg := model.DefaultFreeModelMember(9)
	cfg.Enabled, cfg.Text, cfg.Priority = false, false, 0
	require.NoError(t, db.Create(&cfg).Error)
	got, exists, err := model.GetFreeModelMember(9)
	require.NoError(t, err)
	require.True(t, exists)
	require.False(t, got.Enabled)
	require.False(t, got.Text)
	require.Zero(t, got.Priority)
}
