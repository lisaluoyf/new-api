package service

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
)

func TestSeedance25DefaultsAliasesAndEdit(t *testing.T) {
	normalize := func(input map[string]any) (map[string]any, error) {
		return NormalizeSeedance25Generation(seedanceContext(1), input)
	}
	f, e := normalize(map[string]any{"model": "seedance-2.5", "prompt": "scene"})
	require.NoError(t, e)
	for k, v := range map[string]any{"duration": 5, "aspect_ratio": "adaptive", "resolution": "720p", "generate_audio": true, "watermark": false, "output_format": "mp4", "omni_reference_task_type": "auto", "nsfw_check": false, "return_last_frame": false} {
		require.Equal(t, v, f[k], k)
	}
	f, e = normalize(map[string]any{"prompt": "scene", "draft": true, "audio": false, "seed": 0})
	require.NoError(t, e)
	require.Equal(t, "480p", f["resolution"])
	require.Equal(t, false, f["generate_audio"])
	require.Equal(t, 0, f["seed"])
	f, e = normalize(map[string]any{"prompt": "scene", "omni_reference_task_type": "edit", "video_urls": []any{"https://example.com/source.mp4"}})
	require.NoError(t, e)
	require.Equal(t, -1, f["duration"])
	for _, input := range []map[string]any{{"draft": true, "resolution": "720p"}, {"draft": true, "service_tier": "default"}, {"draft": true, "draft_task_id": "task_x"}, {"duration": 3}, {"duration": 31}, {"omni_reference_task_type": "edit"}, {"omni_reference_task_type": "extend"}, {"omni_reference_task_type": "edit", "duration": 5, "video_urls": []any{"video"}}, {"omni_reference_task_type": "extend", "aspect_ratio": "16:9", "video_urls": []any{"video"}}, {"first_frame_image": "https://example.com/a.png", "ratio": "16:9"}, {"audio": false, "generate_audio": true}, {"metadata": map[string]any{"draft": true, "resolution": "1080p"}}, {"watermark": "false"}} {
		_, e = normalize(input)
		require.Error(t, e, input)
	}
}

func TestSeedanceDraftOwnershipExpirationInheritanceAndAffinity(t *testing.T) {
	db := seedanceTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.Task{}))
	ch := model.Channel{Id: 9, Type: 1, Status: common.ChannelStatusEnabled, Key: "private-key", Models: "seedance-2.5", Group: "default"}
	require.NoError(t, db.Create(&ch).Error)
	require.NoError(t, db.Create(&model.Ability{ChannelId: 9, Group: "default", Model: "seedance-2.5", Enabled: true}).Error)
	model.InitChannelCache()
	task := model.Task{TaskID: "task_public_draft", UserId: 1, ChannelId: 9, CreatedAt: time.Now().Unix(), Status: model.TaskStatusSuccess, Properties: model.Properties{OriginModelName: "seedance-2.5"}, PrivateData: model.TaskPrivateData{Key: "private-key", UpstreamTaskID: "private-draft", SeedanceRequest: map[string]any{"draft": true, "duration": 4, "return_last_frame": true, "watermark": true}}}
	require.NoError(t, db.Create(&task).Error)
	c := seedanceContext(1)
	f, e := NormalizeSeedance25Generation(c, map[string]any{"model": "seedance-2.5", "draft_task_id": task.TaskID})
	require.NoError(t, e)
	require.Equal(t, "1080p", f["resolution"])
	require.Equal(t, 4, f["duration"])
	require.Equal(t, false, f["return_last_frame"])
	require.Equal(t, false, f["watermark"])
	out := ResolveSeedanceDraftRequest(c, f)
	require.Equal(t, "private-draft", out["draft_task_id"])
	require.NotContains(t, out, "duration")
	require.NotContains(t, out, "aspect_ratio")
	require.Equal(t, "task_public_draft", f["draft_task_id"])
	for _, k := range SeedanceDraftInheritedFields {
		_, e = NormalizeSeedance25Generation(seedanceContext(1), map[string]any{"model": "seedance-2.5", "draft_task_id": task.TaskID, k: nil})
		require.Error(t, e, k)
	}
	_, e = NormalizeSeedance25Generation(seedanceContext(2), map[string]any{"draft_task_id": task.TaskID})
	require.ErrorContains(t, e, "your account")
	for _, tc := range []struct {
		field   string
		value   any
		message string
	}{{"created_at", time.Now().Unix() - 7*86400, "expired"}, {"status", model.TaskStatusInProgress, "successfully"}, {"properties", model.Properties{OriginModelName: "seedance-2.0"}, "model does not match"}, {"private_data", model.TaskPrivateData{SeedanceRequest: map[string]any{"draft": false}}, "not a draft"}} {
		require.NoError(t, db.Model(&model.Task{}).Where("id = ?", task.ID).Update(tc.field, tc.value).Error)
		_, e = NormalizeSeedance25Generation(seedanceContext(1), map[string]any{"draft_task_id": task.TaskID})
		require.ErrorContains(t, e, tc.message)
		require.NoError(t, db.Model(&model.Task{}).Where("id = ?", task.ID).Updates(map[string]any{"created_at": task.CreatedAt, "status": task.Status, "properties": task.Properties, "private_data": task.PrivateData}).Error)
	}
	require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", ch.Id).Update("status", common.ChannelStatusManuallyDisabled).Error)
	model.InitChannelCache()
	_, e = NormalizeSeedance25Generation(seedanceContext(1), map[string]any{"draft_task_id": task.TaskID})
	require.ErrorContains(t, e, "same draft_task_id")
}

func TestSeedanceResultAllowlistAndSubmitErrors(t *testing.T) {
	task := &model.Task{TaskID: "task_public", SubmitTime: 100, FinishTime: 110, Status: model.TaskStatusSuccess, Properties: model.Properties{OriginModelName: "seedance-2.5"}, PrivateData: model.TaskPrivateData{ResultURL: "https://private.example/video.mp4", SeedanceRequest: map[string]any{"return_last_frame": true}}, Data: []byte(`{"data":{"usage":{"completion_tokens":123},"cost":99,"result":{"videos":[{"last_frame_url":"https://private.example/frame.png"}]}}}`)}
	out := map[string]any{}
	AddSeedanceResultFields(task, out)
	require.Equal(t, int64(10), out["actual_time"])
	require.Equal(t, int64(123), out["usage"].(map[string]any)["completion_tokens"])
	encoded, e := common.Marshal(out)
	require.NoError(t, e)
	require.NotContains(t, string(encoded), "private.example")
	require.NotContains(t, string(encoded), "cost")
	task.PrivateData.SeedanceRequest["return_last_frame"] = false
	out = map[string]any{}
	AddSeedanceResultFields(task, out)
	require.NotContains(t, out, "last_frame_url")
	task.Status = model.TaskStatusFailure
	out = map[string]any{}
	AddSeedanceResultFields(task, out)
	require.Empty(t, out)
	err := PublicVideoSubmitError(400, []byte(`{"error":{"code":"nsfw_content_detected","message":"Rejected sexual content. key=sk-secret"}}`), "sk-secret", 0)
	require.Equal(t, "nsfw_content_detected", err.Code)
	require.NotContains(t, err.Message, "sk-secret")
	require.Equal(t, 400, err.StatusCode)
}

func TestSeedanceApprovedVideoUsesRecordedDuration(t *testing.T) {
	db := seedanceTestDB(t)
	asset := model.SeedanceResource{ID: "asset_video", Kind: "asset", UserID: 1, Status: "Active", AssetType: "Video", UpstreamID: "private-video", SourceURL: "https://unavailable.example/video.mp4", DurationSeconds: 4}
	require.NoError(t, db.Create(&asset).Error)
	c := seedanceContext(1)
	fields := map[string]any{"prompt": "Edit the colors", "omni_reference_task_type": "edit", "video_urls": []any{"asset://asset_video"}}
	require.NoError(t, ValidateSeedanceVideoInputs(c, fields))
	require.Equal(t, 4, c.GetInt("seedance_video_input_seconds"))
	require.NoError(t, db.Model(&asset).Update("duration_seconds", 3).Error)
	require.ErrorContains(t, ValidateSeedanceVideoInputs(seedanceContext(1), fields), "4 to 30")
	require.Error(t, ValidateSeedanceVideoInputs(seedanceContext(2), fields))
}
