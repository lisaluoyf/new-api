package openai

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func imageTaskTestDB(t *testing.T) {
	t.Helper()
	previous := model.DB
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(&model.Task{}))
	model.DB = db
	t.Cleanup(func() { model.DB = previous; _ = sqlDB.Close() })
}

func TestAsyncImageEditsCreatesQueryableTasksAndPreservesUsage(t *testing.T) {
	imageTaskTestDB(t)
	for _, tc := range []struct {
		name, body string
		status     model.TaskStatus
		outputs    int
	}{
		{"sync gallery", `{"data":[{"url":"https://apimaster.ai/imgs/first.png"},{"url":"https://apimaster.ai/imgs/second.png"}],"usage":{"prompt_tokens":17,"completion_tokens":23,"total_tokens":40}}`, model.TaskStatusSuccess, 2},
		{"upstream task", `{"data":[{"task_id":"upstream-private-id","status":"submitted"}],"usage":{"prompt_tokens":17,"completion_tokens":23,"total_tokens":40}}`, model.TaskStatusSubmitted, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/edits/async", nil)
			c.Set("image_request_body_json", []byte(`{"image":"must-not-be-hedged-as-generation"}`))
			info := &relaycommon.RelayInfo{UserId: 42, OriginModelName: "gpt-image-2.5-flare", RelayMode: relayconstant.RelayModeImagesEdits, ChannelMeta: &relaycommon.ChannelMeta{ChannelId: 73, UpstreamModelName: "gpt-image-2.5-flare", ChannelType: constant.ChannelTypeOpenAI}}
			resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(tc.body))}
			usage, apiErr := OpenaiHandlerWithUsage(c, info, resp)
			require.Nil(t, apiErr)
			require.Equal(t, 17, usage.PromptTokens)
			require.Equal(t, 23, usage.CompletionTokens)
			require.Equal(t, 40, usage.TotalTokens)
			var result struct {
				Data []struct {
					TaskID string `json:"task_id"`
				}
			}
			require.NoError(t, common.Unmarshal(w.Body.Bytes(), &result))
			require.Len(t, result.Data, 1)
			id := result.Data[0].TaskID
			require.True(t, strings.HasPrefix(id, "task_"))
			require.NotEqual(t, "upstream-private-id", id)
			task, found, err := model.GetByOnlyTaskId(id)
			require.NoError(t, err)
			require.True(t, found)
			require.Equal(t, 42, task.UserId)
			require.Equal(t, 73, task.ChannelId)
			require.Equal(t, tc.status, task.Status)
			require.Len(t, task.PrivateData.ImageResultURLs, tc.outputs)
			require.Zero(t, task.PrivateData.HedgeChannelId)
			if tc.outputs > 0 {
				require.Equal(t, []string{"https://apimaster.ai/imgs/first.png", "https://apimaster.ai/imgs/second.png"}, task.PrivateData.ImageResultURLs)
			} else {
				require.Equal(t, "upstream-private-id", task.PrivateData.UpstreamTaskID)
			}
		})
	}
}

func TestAsyncEditUpstreamPaths(t *testing.T) {
	for _, channelID := range []int{73, 102, 149} {
		info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeImagesEdits, OriginModelName: "gpt-image-2.5-flare", RequestURLPath: "/v1/images/edits/async", ChannelMeta: &relaycommon.ChannelMeta{ChannelId: channelID, ChannelType: constant.ChannelTypeOpenAI, ChannelBaseUrl: "https://upstream.example"}}
		a := &Adaptor{}
		a.Init(info)
		url, err := a.GetRequestURL(info)
		require.NoError(t, err)
		require.Equal(t, "https://upstream.example/v1/images/edits", url)
		require.Equal(t, "/v1/images/edits/async", info.RequestURLPath)
	}
	for _, submitMode := range []string{"", "generations", "generations_async"} {
		require.Equal(t, "/v1/images/edits", normalizeImageGenerationsRequestPath("/v1/images/edits/async", "", relayconstant.RelayModeImagesEdits, "gpt-image-2.5-flare", submitMode))
	}
}

func TestAsyncEditsDoesNotReturnUntrackedResults(t *testing.T) {
	imageTaskTestDB(t)
	require.NoError(t, model.DB.Migrator().DropTable(&model.Task{}))
	for _, body := range []string{
		`{"data":[{"task_id":"private-upstream-task","status":"submitted"}]}`,
		`{"data":[{"url":"https://apimaster.ai/imgs/result.png"}]}`,
		`{"data":[]}`,
	} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/edits/async", nil)
		info := &relaycommon.RelayInfo{UserId: 42, OriginModelName: "gpt-image-2.5-flare", RelayMode: relayconstant.RelayModeImagesEdits, ChannelMeta: &relaycommon.ChannelMeta{ChannelId: 73, ChannelType: constant.ChannelTypeOpenAI}}
		resp := &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}
		_, apiErr := OpenaiHandlerWithUsage(c, info, resp)
		require.NotNil(t, apiErr)
		require.True(t, types.IsSkipRetryError(apiErr), "never resubmit a consumed upstream request after persistence failure")
		require.Empty(t, w.Body.String(), "must not expose an untracked upstream task or a raw image as a successful task response")
	}
}
