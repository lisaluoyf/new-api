package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

func TestSeedancePublicErrorsPreserveOfficialCodes(t *testing.T) {
	for _, tc := range []struct{ code, message, body string }{
		{"InputImageSensitiveContentDetected", "The input image may contain sensitive information. Please replace it and try again.", `{"error":{"code":"InputImageSensitiveContentDetected","message":"The input image may contain sensitive information. Please replace it and try again."}}`},
		{"InputImageSensitiveContentDetected.PolicyViolation", "The request failed because the input image may be related to copyright restrictions.", `{"data":{"error":{"code":"nsfw_content_detected","message":"Review failed","upstream":{"code":"InputImageSensitiveContentDetected.PolicyViolation","message":"The request failed because the input image may be related to copyright restrictions."}}}}`},
		{"OutputVideoSensitiveContentDetected", "The output video may contain sensitive information.", `{"error":{"code":"OutputVideoSensitiveContentDetected","message":"The output video may contain sensitive information."}}`},
		{"InputImageSensitiveContentDetected.PrivacyInformation", "The input image may contain real person.", `{"response":{"error":{"code":"InputImageSensitiveContentDetected.PrivacyInformation","message":"The input image may contain real person."}}}`},
		{"InvalidParameter.TaskTypeMismatch", "ratio must be adaptive", `{"error":{"code":"InvalidParameter.TaskTypeMismatch","message":"ratio must be adaptive"}}`},
	} {
		t.Run(tc.code, func(t *testing.T) {
			task := &Task{Status: TaskStatusFailure, Properties: Properties{OriginModelName: "seedance-2.5"}, FailReason: "Task failed", Data: []byte(tc.body)}
			original := string(task.Data)
			public := PublicTaskError(task)
			require.Equal(t, tc.code, public.Code)
			require.Equal(t, tc.message, public.Message)
			require.Equal(t, original, string(task.Data), "Historical evidence must remain unchanged")
			require.Equal(t, "Task failed", task.FailReason)
		})
	}
}

func TestSeedancePublicErrorsSafeFallbackAndFinalRecovery(t *testing.T) {
	task := &Task{Status: TaskStatusFailure, Properties: Properties{OriginModelName: "seedance-2.0"}, FailReason: "Task failed", PrivateData: TaskPrivateData{Key: "private-secret"}}
	for _, code := range []string{"sk-private-secret", "https://private.example/secret", "UnknownProviderSecret", "InvalidParameter.private-secret", "InvalidParameter.private_secret"} {
		task.PrivateData.Key = "private_secret"
		body, err := common.Marshal(map[string]any{"error": map[string]any{"code": code, "message": "Invalid input; Bearer private-secret https://private.example/image.png"}})
		require.NoError(t, err)
		task.Data = body
		public := PublicTaskError(task)
		require.Equal(t, "task_failed", public.Code)
		require.NotContains(t, public.Message, "private-secret")
		require.NotContains(t, public.Message, "private.example")
	}
	task.Data = []byte(`{"error":{"code":"OutputVideoSensitiveContentDetected","message":"The output video may contain sensitive information."}}`)
	task.FailReason = "Backup channel material review timed out"
	task.PrivateData.SeedanceFallback = &SeedanceFallbackRequest{Started: true, Stage: "failed"}
	public := PublicTaskError(task)
	require.Equal(t, "asset_review_timeout", public.Code)
	require.Equal(t, task.FailReason, public.Message)
	task.Status = TaskStatusRetrying
	require.Nil(t, PublicTaskError(task), "Recovery in progress must not report a terminal error")
	require.Empty(t, PublicTaskFailure(task))
	require.Nil(t, PublicTaskError(nil))
}

func TestSeedanceExplicitTextErrorAndDetailedReasonPrecedence(t *testing.T) {
	task := &Task{Status: TaskStatusFailure, Properties: Properties{OriginModelName: "seedance-2.0"},
		FailReason: "InputImageSensitiveContentDetected: The input image may contain sensitive information. Bearer private-secret",
		Data:       []byte(`{"error":{"code":"generation_failed","message":"Video generation failed"}}`)}
	public := PublicTaskError(task)
	require.Equal(t, "InputImageSensitiveContentDetected", public.Code)
	require.Equal(t, "The input image may contain sensitive information. [redacted]", public.Message)
	task.FailReason = "Image width must be at least 300 pixels"
	require.Equal(t, "task_failed", PublicTaskError(task).Code)
	require.Equal(t, task.FailReason, PublicTaskError(task).Message)
	task.Data = []byte(`{"error":{"code":"InvalidParameter","message":"Task failed"}}`)
	require.Equal(t, "InvalidParameter", PublicTaskError(task).Code)
	require.Equal(t, task.FailReason, PublicTaskError(task).Message)
}

func TestSeedanceWebhookUsesSameErrorWithinTransaction(t *testing.T) {
	db := webhookTestDB(t) // Deliberately limited to a single SQLite connection.
	require.NoError(t, db.AutoMigrate(&Channel{}, &SeedanceResource{}))
	require.NoError(t, db.Create(&Channel{Id: 7, Name: "private-provider", Key: "private-key"}).Error)
	require.NoError(t, db.Create(&SeedanceResource{ID: "asset_public", UserID: 1, Kind: "asset", UpstreamID: "hidden-material"}).Error)
	task := Task{TaskID: GenerateTaskID(), UserId: 1, ChannelId: 7, Status: TaskStatusFailure,
		Properties:  Properties{OriginModelName: "seedance-2.0-mini"},
		PrivateData: TaskPrivateData{Webhook: &TaskWebhookConfig{EndpointID: "ep"}, RequestData: `{"image_urls":["asset://asset_public"]}`},
		Data:        []byte(`{"data":{"error":{"upstream":{"code":"InputImageSensitiveContentDetected","message":"The input image may contain sensitive information. private-provider private-key hidden-material https://private.example/image.png"}}}}`)}
	require.NoError(t, task.Insert())
	var event TaskWebhookEvent
	require.NoError(t, db.First(&event).Error)
	var payload struct {
		Data struct {
			Error struct{ Code, Message string }
		}
	}
	require.NoError(t, common.UnmarshalJsonStr(event.Payload, &payload))
	public := PublicTaskError(&task)
	require.Equal(t, public.Code, payload.Data.Error.Code)
	require.Equal(t, public.Message, payload.Data.Error.Message)
	for _, secret := range []string{"private-provider", "private-key", "hidden-material", "private.example"} {
		require.NotContains(t, event.Payload, secret)
	}
}
