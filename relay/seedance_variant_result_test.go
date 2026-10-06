package relay

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestSeedanceVariantCompatibleCompletionIncludesOwnedLastFrame(t *testing.T) {
	for _, name := range []string{"seedance-2.0", "seedance-2.5", "seedance-2.0-fast", "seedance-2.0-mini"} {
		task := &model.Task{TaskID: "task_public", Status: model.TaskStatusSuccess, SubmitTime: 100, FinishTime: 142, Properties: model.Properties{OriginModelName: name}, PrivateData: model.TaskPrivateData{ResultURL: "https://private.example/video.mp4", SeedanceRequest: map[string]any{"return_last_frame": true}}, Data: []byte(`{"data":{"cost":123,"result":{"last_frame_url":"https://private.example/frame.jpg"}}}`)}
		raw := buildSafeVideoTaskResponse(task, task.Data)
		var response map[string]any
		require.NoError(t, common.Unmarshal(raw, &response))
		data := response["data"].(map[string]any)
		require.Equal(t, 42.0, data["actual_time"])
		require.Contains(t, data["last_frame_url"], "/v1/videos/task_public/last-frame")
		require.Contains(t, data["url"], "/v1/videos/task_public/content")
		require.NotContains(t, string(raw), "private.example")
		require.NotContains(t, data, "cost")
		require.NotContains(t, data, "usage")
	}
}
