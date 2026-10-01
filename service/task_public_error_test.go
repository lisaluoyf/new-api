package service

import (
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
