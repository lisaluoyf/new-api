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
