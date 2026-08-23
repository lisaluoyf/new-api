package controller

import (
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
)

func TestCopySafeVideoHeadersUsesAllowlist(t *testing.T) {
	src := http.Header{
		"Content-Type":                 []string{"video/mp4"},
		"Content-Length":               []string{"123"},
		"Etag":                         []string{"safe-etag"},
		"Via":                          []string{"cloudfront.net"},
		"X-Amz-Cf-Id":                  []string{"provider-id"},
		"X-Amz-Server-Side-Encryption": []string{"AES256"},
		"X-Cache":                      []string{"Hit from cloudfront"},
		"X-Oneapi-Request-Id":          []string{"upstream-request"},
		"Content-Disposition":          []string{`attachment; filename="provider-task.mp4"`},
	}
	dst := make(http.Header)

	copySafeVideoHeaders(dst, src)

	require.Equal(t, "video/mp4", dst.Get("Content-Type"))
	require.Equal(t, "123", dst.Get("Content-Length"))
	require.Equal(t, "safe-etag", dst.Get("ETag"))
	for _, key := range []string{"Via", "X-Amz-Cf-Id", "X-Amz-Server-Side-Encryption", "X-Cache", "X-Oneapi-Request-Id", "Content-Disposition"} {
		require.Empty(t, dst.Get(key), key)
	}
}

func TestTasksToUserDtoOmitsProviderAndBillingData(t *testing.T) {
	task := &model.Task{
		ID:         99,
		TaskID:     "task_public",
		Platform:   constant.TaskPlatformApimartVideo,
		UserId:     42,
		Group:      "default",
		ChannelId:  7,
		Quota:      1234,
		Action:     constant.TaskActionGenerate,
		Status:     model.TaskStatusSuccess,
		FailReason: "https://provider.example/raw.mp4",
		Properties: model.Properties{OriginModelName: "kling-v3-omni", UpstreamModelName: "provider-model"},
		PrivateData: model.TaskPrivateData{
			UpstreamTaskID: "provider-task-id",
			ResultURL:      "https://provider.example/raw.mp4",
		},
		Data: []byte(`{"id":"provider-task-id","cost":9.9,"url":"https://provider.example/raw.mp4"}`),
	}

	items := tasksToUserDto([]*model.Task{task})
	require.Len(t, items, 1)
	require.Equal(t, "video", items[0].Platform)
	require.Equal(t, "kling-v3-omni", items[0].Model)
	require.Contains(t, items[0].ResultURL, "/v1/videos/task_public/content")

	body, err := common.Marshal(items[0])
	require.NoError(t, err)
	text := string(body)
	for _, forbidden := range []string{"provider-task-id", "provider.example", "provider-model", "channel_id", "quota", "user_id", "properties", "\"data\""} {
		require.NotContains(t, text, forbidden)
	}
}

func TestTasksToUserDtoHidesProviderFailureAndLegacyResult(t *testing.T) {
	task := &model.Task{
		TaskID:     "task_failed",
		Platform:   constant.TaskPlatformApimartVideo,
		Status:     model.TaskStatusFailure,
		FailReason: "The provider width should not be less than 700px",
	}

	item := tasksToUserDto([]*model.Task{task})[0]
	require.Equal(t, "Task failed", item.FailReason)
	require.Empty(t, item.ResultURL)
}
