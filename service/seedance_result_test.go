package service

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
)

func TestSeedanceArkLastFrameResult(t *testing.T) {
	for _, payload := range []string{
		`{"content":{"last_frame_url":"https://private.example/frame.jpg"}}`,
		`{"data":{"content":{"last_frame_url":"https://private.example/frame.jpg"}}}`,
		`{"data":{"data":{"content":{"last_frame_url":"https://private.example/frame.jpg"}}}}`,
	} {
		t.Run(payload, func(t *testing.T) {
			task := &model.Task{TaskID: "task_public", ChannelId: 292, Status: model.TaskStatusSuccess, Properties: model.Properties{OriginModelName: "seedance-2.5"}, PrivateData: model.TaskPrivateData{SeedanceRequest: map[string]any{"return_last_frame": true}}, Data: []byte(payload)}
			require.Equal(t, "https://private.example/frame.jpg", SeedanceLastFrameSource(task))
			out := map[string]any{}
			AddSeedanceResultFields(task, out)
			require.Contains(t, out["last_frame_url"], "/v1/videos/task_public/last-frame")
			encoded, err := common.Marshal(out)
			require.NoError(t, err)
			require.NotContains(t, string(encoded), "private.example")
			task.PrivateData.SeedanceRequest["return_last_frame"] = false
			require.Empty(t, SeedanceLastFrameSource(task))
			task.PrivateData.SeedanceRequest["return_last_frame"] = true
			task.Status = model.TaskStatusInProgress
			require.Empty(t, SeedanceLastFrameSource(task))
			task.Status = model.TaskStatusSuccess
			task.Data = []byte(`{"content":{"last_frame_url":"file:///etc/passwd"}}`)
			require.Empty(t, SeedanceLastFrameSource(task))
		})
	}
}
