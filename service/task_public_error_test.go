package service

import (
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestSanitizeTaskFailureRetainsActionableErrors(t *testing.T) {
	for _, reason := range []string{
		"The parameter ratio specified in the request is not valid. For first-frame or first-last-frame generation, the output ratio follows the first-frame image.",
		"first_frame: image dimensions must each be 300-6000 pixels (got 200x640)",
		"duration must be -1 or between 4 and 30 seconds",
		"assets[0] audio duration 16.00s exceeds the limit of 15s",
		"Your request was rejected by the safety system.",
		"图片尺寸不符合要求，请提供宽高至少 300 像素的图片。",
	} {
		require.Equal(t, reason, sanitizeTaskFailure(reason))
	}
}

func TestSanitizeTaskFailureRemovesSensitiveDiagnostics(t *testing.T) {
	cases := []struct {
		input   string
		secrets []string
	}{
		{`ratio is invalid; https://user:password@api.provider.example/v1/tasks?id=private&signature=secret`, []string{"password", "provider.example", "signature", "private"}},
		{`ratio invalid; Authorization: Bearer private-access; api_key="quoted-secret"; cookie: session=secret-cookie`, []string{"private-access", "quoted-secret", "secret-cookie"}},
		{`ratio invalid; access_token=private-token; password: 'my password'; X-Secret: merchant-secret`, []string{"private-token", "my password", "merchant-secret"}},
		{`ratio invalid; endpoint [2001:db8::1]`, []string{"2001:db8::1"}},
		{`ratio invalid; sk-fake-secret-value API_KEY_UNLABELLED 192.168.1.1 support@provider.example request_id=internal-id /opt/secret/file`, []string{"sk-fake-secret-value", "192.168.1.1", "support@provider.example", "internal-id", "/opt/secret/file"}},
		{`upstream HTTP 400: {"error":{"message":"ratio invalid","code":"InvalidParameter"},"headers":{"Authorization":"Bearer secret"},"url":"https://provider.example"}`, []string{"secret", "provider.example", "headers", "Authorization"}},
	}
	for _, tc := range cases {
		result := sanitizeTaskFailure(tc.input)
		require.Contains(t, result, "ratio")
		for _, secret := range tc.secrets {
			require.NotContains(t, result, secret)
		}
	}
	require.Equal(t, "Task failed", sanitizeTaskFailure(`upstream: {malformed sensitive response`))
	require.Equal(t, "Task failed", sanitizeTaskFailure(""))
	require.Equal(t, "Task failed", sanitizeTaskFailure("Bearer token"))
	require.NotContains(t, sanitizeTaskFailure("invalid ratio; private-id secret-value", "private-id", "secret-value"), "secret-value")
}

func TestPublicTaskFailureDoesNotMutateAudit(t *testing.T) {
	task := &model.Task{Status: model.TaskStatusFailure, FailReason: "ratio invalid; provider-id actual-secret private-model", Properties: model.Properties{OriginModelName: "public-model", UpstreamModelName: "private-model"}, PrivateData: model.TaskPrivateData{Key: "actual-secret", UpstreamTaskID: "provider-id"}}
	original := task.FailReason
	reason := PublicTaskFailure(task)
	require.Contains(t, reason, "ratio invalid")
	for _, secret := range []string{"provider-id", "actual-secret", "private-model"} {
		require.NotContains(t, reason, secret)
	}
	require.Equal(t, original, task.FailReason)
	task.Status = model.TaskStatusSuccess
	require.Empty(t, PublicTaskFailure(task))
}

func TestPublicTaskFailurePreservesProviderReviewReasons(t *testing.T) {
	for _, tc := range []struct {
		name, reason string
		nested       bool
	}{
		{"first input image", "The request failed because the input image may be related to copyright restrictions. Request ID: 20261011163219F021CD55F251F078E959_asset-20261011163219-tp2zd", true},
		{"second input image", "The request failed because the input image may be related to copyright restrictions. Request ID: 20261011163934D6F09AA2463AD98BD20B_asset-20261011163934-dlp79", true},
		{"output sensitive", "The request failed because the output video may contain sensitive information. Request id: 02179170959940900000000000000000000ffffac1926adbf679f", false},
		{"output copyright", "The request failed because the output video may be related to copyright restrictions. Request id: 02179170790151900000000000000000000ffffac175b6997935f", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			task := model.Task{Status: model.TaskStatusFailure, FailReason: tc.reason}
			if tc.nested {
				task.Platform = "apimart-video"
				task.FailReason = "Your request did not pass content safety review. Please adjust your prompt or input files and try again."
				body, err := common.Marshal(map[string]any{"data": map[string]any{"error": map[string]any{
					"message": task.FailReason, "param": "image_with_roles[0].url",
					"upstream": map[string]any{"code": "InputImageSensitiveContentDetected.PolicyViolation", "message": tc.reason},
				}}})
				require.NoError(t, err)
				task.Data = body
			}
			originalReason, originalData := task.FailReason, string(task.Data)
			require.Equal(t, tc.reason, PublicTaskFailure(&task))
			require.Equal(t, originalReason, task.FailReason)
			require.Equal(t, originalData, string(task.Data))
		})
	}
}

func TestPublicTaskFailureIgnoresUnrelatedNestedDiagnostics(t *testing.T) {
	task := model.Task{Platform: "apimart-video", Status: model.TaskStatusFailure, FailReason: "Task failed"}
	for _, body := range []string{
		`{"data":{"error":{"upstream":{"code":"Unauthorized","message":"secret diagnostic"}}}}`,
		`{"data":{"error":{"upstream":{"code":"InputImageSensitiveContentDetected.PolicyViolation","message":"secret diagnostic"}}}}`,
		`{"data":{"error":{"upstream":{"message":"may contain sensitive information"}}}}`,
		`{malformed`,
	} {
		task.Data = []byte(body)
		require.Equal(t, "Task failed", PublicTaskFailure(&task))
	}
}

func TestReviewReasonsStillRemoveCredentialsAndInternalDiagnostics(t *testing.T) {
	reason := "The output video may contain sensitive information. Request id: 02179170959940900000000000000000000ffffac1926adbf679f; Authorization: Bearer private-token; https://provider.example/private; channel_id=153; asset_id=private-asset; /opt/private"
	result := sanitizeTaskFailure(reason)
	require.Contains(t, result, "Request id: 02179170959940900000000000000000000ffffac1926adbf679f")
	for _, secret := range []string{"private-token", "provider.example", "channel_id", "private-asset", "/opt/private"} {
		require.NotContains(t, result, secret)
	}
	require.NotContains(t, sanitizeTaskFailure("ordinary failure; Request id: 02179170959940900000000000000000000ffffac1926adbf679f"), "021791709599")
	require.NotContains(t, sanitizeTaskFailure("may contain sensitive information; request_id=sk-fake-secret-value"), "sk-fake-secret-value")
	require.NotContains(t, sanitizeTaskFailure("may contain sensitive information; request_id=private-provider-id", "private-provider-id"), "private-provider-id")
}

func TestEnrichUserTaskLogFailuresBackfillsHistoryWithOwnership(t *testing.T) {
	oldDB := model.DB
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	t.Cleanup(func() { model.DB = oldDB })
	require.NoError(t, db.AutoMigrate(&model.Task{}))
	task := model.Task{TaskID: "task_public", UserId: 42, Status: model.TaskStatusFailure, FailReason: "ratio invalid; Bearer private-token"}
	require.NoError(t, db.Create(&task).Error)
	logs := []*model.Log{
		{UserId: 42, Type: model.LogTypeConsume, Other: `{"task_id":"task_public"}`},
		{UserId: 42, Type: model.LogTypeRefund, Other: `{"task_id":"task_public"}`},
		{UserId: 43, Type: model.LogTypeConsume, Other: `{"task_id":"task_public","task_fail_reason":"untrusted raw reason"}`},
	}
	EnrichUserTaskLogFailures(logs)
	for _, log := range logs[:2] {
		other, _ := common.StrToMap(log.Other)
		require.Contains(t, other["task_fail_reason"], "ratio invalid")
		require.NotContains(t, log.Other, "private-token")
	}
	require.NotContains(t, logs[2].Other, "task_fail_reason")
	var stored model.Task
	require.NoError(t, db.First(&stored, task.ID).Error)
	require.Equal(t, task.FailReason, stored.FailReason)
}

func TestEnrichUserTaskLogFailuresShowsHistoricalNestedReview(t *testing.T) {
	oldDB := model.DB
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	t.Cleanup(func() { model.DB = oldDB })
	require.NoError(t, db.AutoMigrate(&model.Task{}))
	reason := "The request failed because the input image may be related to copyright restrictions. Request ID: 20261011163219F021CD55F251F078E959_asset-20261011163219-tp2zd"
	body, err := common.Marshal(map[string]any{"data": map[string]any{"error": map[string]any{"upstream": map[string]any{"code": "InputImageSensitiveContentDetected.PolicyViolation", "message": reason}}}})
	require.NoError(t, err)
	task := model.Task{TaskID: "task_review", UserId: 42, Platform: "apimart-video", Status: model.TaskStatusFailure, FailReason: "Your request did not pass content safety review.", Data: body}
	require.NoError(t, db.Create(&task).Error)
	logs := []*model.Log{
		{UserId: 42, Type: model.LogTypeConsume, Other: `{"task_id":"task_review","billing_refunded":true}`},
		{UserId: 42, Type: model.LogTypeRefund, Other: `{"task_id":"task_review"}`},
		{UserId: 43, Type: model.LogTypeConsume, Other: `{"task_id":"task_review"}`},
	}
	EnrichUserTaskLogFailures(logs)
	for _, log := range logs[:2] {
		other, _ := common.StrToMap(log.Other)
		require.Equal(t, reason, other["task_fail_reason"])
		require.True(t, strings.HasPrefix(log.Content, "Task failed: "+reason))
		require.Contains(t, log.Content, "upstream_error")
		require.Contains(t, log.Other, "InputImageSensitiveContentDetected.PolicyViolation")
	}
	require.NotContains(t, logs[2].Other, "copyright")
	other, _ := common.StrToMap(logs[0].Other)
	require.Equal(t, true, other["billing_refunded"])
	var stored model.Task
	require.NoError(t, db.First(&stored, task.ID).Error)
	require.Equal(t, task.FailReason, stored.FailReason)
	require.Equal(t, string(body), string(stored.Data))
}
