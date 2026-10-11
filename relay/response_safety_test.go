package relay

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel/task/taskcommon"
	"github.com/stretchr/testify/require"
)

func TestBuildSafeVideoTaskResponseOmitsProviderData(t *testing.T) {
	task := &model.Task{
		TaskID:     "task_public",
		Status:     model.TaskStatusSuccess,
		Progress:   "100%",
		FailReason: "https://provider.example/raw.mp4",
		PrivateData: model.TaskPrivateData{
			UpstreamTaskID: "provider-task-id",
			ResultURL:      "https://provider.example/raw.mp4",
		},
		Data: []byte(`{"id":"provider-task-id","cost":3.61,"url":"https://provider.example/raw.mp4"}`),
	}
	body := buildSafeVideoTaskResponse(task, task.Data)

	var decoded map[string]any
	require.NoError(t, common.Unmarshal(body, &decoded))
	data := decoded["data"].(map[string]any)
	require.Equal(t, "task_public", data["task_id"])
	require.Equal(t, taskcommon.BuildProxyURL("task_public"), data["url"])
	text := string(body)
	for _, forbidden := range []string{"provider-task-id", "provider.example", "\"cost\"", "channel", "upstream"} {
		require.NotContains(t, text, forbidden)
	}
}

func TestBuildSafeVideoTaskResponsePreservesSafeFailure(t *testing.T) {
	task := &model.Task{
		TaskID:     "task_public",
		Status:     model.TaskStatusFailure,
		FailReason: "width should not be less than 700px; https://provider.example/internal?key=secret",
	}
	body := buildSafeVideoTaskResponse(task, []byte(`{"error":{"message":"provider-specific"}}`))

	require.Contains(t, string(body), "width should not be less than 700px")
	require.NotContains(t, string(body), "provider.example")
	require.NotContains(t, string(body), "secret")
}

func TestSanitizeOpenAIVideoTaskFailurePreservesPublicShape(t *testing.T) {
	task := &model.Task{Status: model.TaskStatusFailure, FailReason: "ratio invalid; Bearer secret-token"}
	result, err := sanitizeOpenAIVideoTaskFailure(task, []byte(`{"id":"task_public","status":"failed","error":{"message":"raw provider error","code":"provider-code"}}`))
	require.NoError(t, err)
	require.Contains(t, string(result), "ratio invalid")
	require.Contains(t, string(result), "task_public")
	require.NotContains(t, string(result), "secret-token")
	require.NotContains(t, string(result), "provider-code")
}

func TestRealtimeFetchDoesNotRequeryTerminalTasks(t *testing.T) {
	for _, status := range []model.TaskStatus{model.TaskStatusSuccess, model.TaskStatusFailure} {
		task := &model.Task{Status: status}
		// No DB/channel is needed: terminal tasks must return before any query.
		require.Nil(t, tryRealtimeFetch(task, true))
		require.Equal(t, status, task.Status)
	}
}

func TestVideoResponsesExposeNestedReviewWithoutProviderPayload(t *testing.T) {
	reason := "The request failed because the input image may be related to copyright restrictions. Request ID: 20261011163219F021CD55F251F078E959_asset-20261011163219-tp2zd"
	data, err := common.Marshal(map[string]any{"data": map[string]any{
		"cost": 123, "id": "provider-task-id", "error": map[string]any{
			"upstream": map[string]any{"code": "InputImageSensitiveContentDetected.PolicyViolation", "message": reason},
		},
	}})
	require.NoError(t, err)
	task := &model.Task{TaskID: "task_public", Platform: "apimart-video", Status: model.TaskStatusFailure,
		Properties: model.Properties{OriginModelName: "seedance-2.5"},
		FailReason: "Your request did not pass content safety review.", Data: data}
	compatibility := buildSafeVideoTaskResponse(task, data)
	openAI, err := sanitizeOpenAIVideoTaskFailure(task, []byte(`{"id":"task_public","status":"failed"}`))
	require.NoError(t, err)
	for _, body := range [][]byte{compatibility, openAI} {
		require.Contains(t, string(body), reason)
		for _, forbidden := range []string{"provider-task-id", `"cost"`, `"upstream"`, `"private_data"`} {
			require.NotContains(t, string(body), forbidden)
		}
	}
}
