package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/stretchr/testify/require"
)

func videoFeeTestHeaders(raw []byte, id, secret string, now int64) http.Header {
	stamp := strconv.FormatInt(now, 10)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(stamp + "." + id + "."))
	mac.Write(raw)
	return http.Header{"X-Webhook-Event-Id": {id}, "X-Webhook-Timestamp": {stamp}, "X-Webhook-Key-Id": {"v1"}, "X-Webhook-Signature": {"v1=" + hex.EncodeToString(mac.Sum(nil))}}
}
func TestVideoFeeWebhookSignature(t *testing.T) {
	raw := []byte(`{"event_id":"event","data":{"status":"SUCCESS"}}`)
	now := time.Now().Unix()
	headers := videoFeeTestHeaders(raw, "event", "not-hex-decoded", now)
	require.True(t, VerifyVideoFeeWebhook("not-hex-decoded", headers, raw, now))
	require.False(t, VerifyVideoFeeWebhook("not-hex-decoded", headers, append(raw, ' '), now))
	require.False(t, VerifyVideoFeeWebhook("wrong", headers, raw, now))
	require.False(t, VerifyVideoFeeWebhook("not-hex-decoded", headers, raw, now+301))
	headers.Set("X-Webhook-Key-Id", "v2")
	require.False(t, VerifyVideoFeeWebhook("not-hex-decoded", headers, raw, now))
	headers.Set("X-Webhook-Key-Id", "v1")
	headers.Set("X-Webhook-Event-Id", "other")
	require.False(t, VerifyVideoFeeWebhook("not-hex-decoded", headers, raw, now))
}

type videoFeeTestAdaptor struct{ mockAdaptor }

func (a *videoFeeTestAdaptor) ParseTaskResult(raw []byte) (*relaycommon.TaskInfo, error) {
	var p videoFeeWebhookPayload
	if err := common.Unmarshal(raw, &p); err != nil {
		return nil, err
	}
	status := p.Data.Status
	if status == "CANCELLED" {
		status = "FAILURE"
	}
	return &relaycommon.TaskInfo{TaskID: p.Data.TaskID, Status: status, Url: "https://example.com/video.mp4", Reason: "generation failed"}, nil
}
func TestVideoFeeWebhookInboxTerminalDedupAndRecovery(t *testing.T) {
	truncate(t)
	oldClient := httpClient
	httpClient = http.DefaultClient
	t.Cleanup(func() { httpClient = oldClient })
	setupTaskWebhookTest(t)
	require.NoError(t, model.DB.AutoMigrate(&model.VideoFeeWebhookInbox{}))
	require.NoError(t, model.DB.Where("1=1").Delete(&model.VideoFeeWebhookInbox{}).Error)
	t.Cleanup(func() { model.DB.Where("1=1").Delete(&model.VideoFeeWebhookInbox{}) })
	secret := "provider-team-secret"
	keyCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/webhooks/signing-key", r.URL.Path)
		require.Equal(t, "Bearer supplier-key", r.Header.Get("Authorization"))
		keyCalls++
		fmt.Fprintf(w, `{"key_id":"v1","algorithm":"HMAC-SHA256","signing_key":%q}`, secret)
	}))
	defer server.Close()
	ch := model.Channel{Id: 273, Type: 1, Key: "supplier-key", BaseURL: &server.URL}
	require.NoError(t, model.DB.Create(&ch).Error)
	old := GetTaskAdaptorFunc
	GetTaskAdaptorFunc = func(constant.TaskPlatform) TaskPollingAdaptor { return &videoFeeTestAdaptor{} }
	t.Cleanup(func() { GetTaskAdaptorFunc = old })
	seedUser(t, 91273, 10000)
	seedToken(t, 91273, 91273, "test-customer-key", 10000)
	for _, status := range []string{"SUCCESS", "FAILURE", "CANCELLED"} {
		task := makeTask(91273, 273, 100, 91273, BillingSourceWallet, 0)
		task.TaskID = "public-" + status
		task.Platform = constant.TaskPlatformVideoFee
		task.Status = model.TaskStatusSubmitted
		task.PrivateData.UpstreamTaskID = "supplier-" + status
		task.Properties.OriginModelName = "seedance-2.5"
		require.NoError(t, model.DB.Create(task).Error)
		kind := "video.task.completed"
		if status != "SUCCESS" {
			kind = "video.task.failed"
		}
		id := "event-" + status
		raw := []byte(fmt.Sprintf(`{"event_id":%q,"event_type":%q,"data":{"task_id":%q,"status":%q,"result_url":"https://example.com/video.mp4"}}`, id, kind, task.PrivateData.UpstreamTaskID, status))
		headers := videoFeeTestHeaders(raw, id, secret, time.Now().Unix())
		require.NoError(t, ReceiveVideoFeeWebhook(context.Background(), headers, raw))
		require.NoError(t, ReceiveVideoFeeWebhook(context.Background(), headers, raw))
		var count int64
		require.NoError(t, model.DB.Model(&model.VideoFeeWebhookInbox{}).Where("id = ?", id).Count(&count).Error)
		require.EqualValues(t, 1, count)
		// Simulate a worker crash with an expired processing lease.
		require.NoError(t, model.DB.Model(&model.VideoFeeWebhookInbox{}).Where("id = ?", id).Update("lease_until", time.Now().Unix()-1).Error)
		quotaBefore := getUserQuota(t, 91273)
		require.NoError(t, RunVideoFeeWebhookBatch())
		var fresh model.Task
		require.NoError(t, model.DB.First(&fresh, task.ID).Error)
		expected := model.TaskStatusSuccess
		if status != "SUCCESS" {
			expected = model.TaskStatusFailure
		}
		require.EqualValues(t, expected, fresh.Status)
		if status != "SUCCESS" {
			require.Equal(t, quotaBefore+100, getUserQuota(t, 91273))
		}
		quotaAfter := getUserQuota(t, 91273)
		// Re-delivery and polling must not charge/refund or enqueue SD twice.
		require.NoError(t, ReceiveVideoFeeWebhook(context.Background(), headers, raw))
		require.NoError(t, RunVideoFeeWebhookBatch())
		require.Equal(t, quotaAfter, getUserQuota(t, 91273))
		if status == "SUCCESS" {
			require.NoError(t, model.DB.Model(&model.VideoVerificationRun{}).Where("task_id = ?", task.ID).Count(&count).Error)
			require.EqualValues(t, 1, count)
		}
		var inbox model.VideoFeeWebhookInbox
		require.NoError(t, model.DB.First(&inbox, "id = ?", id).Error)
		require.Equal(t, "processed", inbox.Status)
		// A second supplier event after polling reached terminal is harmless.
		later := inbox
		later.ID = id + "-late"
		later.Status = "pending"
		later.ProcessedAt = 0
		later.NextAt = time.Now().Unix()
		later.LeaseUntil = 0
		require.NoError(t, model.DB.Create(&later).Error)
		require.NoError(t, RunVideoFeeWebhookBatch())
		require.Equal(t, quotaAfter, getUserQuota(t, 91273))
	}
	require.GreaterOrEqual(t, keyCalls, 1)
	// Generic unsigned callback must not bypass supplier authentication.
	require.Error(t, ProcessMediaTaskWebhook(context.Background(), []byte(`{"id":"public-SUCCESS","status":"SUCCESS"}`)))
	bad := []byte(`{"event_id":"bad","event_type":"video.task.completed","data":{"task_id":"supplier-SUCCESS","status":"SUCCESS"}}`)
	require.ErrorIs(t, ReceiveVideoFeeWebhook(context.Background(), videoFeeTestHeaders(bad, "bad", "wrong", time.Now().Unix()), bad), ErrVideoFeeWebhookAuth)
	var count int64
	require.NoError(t, model.DB.Model(&model.VideoFeeWebhookInbox{}).Where("id = ?", "bad").Count(&count).Error)
	require.Zero(t, count)
	// A public gateway ID is not an upstream platform ID.
	wrongID := []byte(`{"event_id":"wrong-id","event_type":"video.task.completed","data":{"task_id":"public-SUCCESS","status":"SUCCESS"}}`)
	require.ErrorIs(t, ReceiveVideoFeeWebhook(context.Background(), videoFeeTestHeaders(wrongID, "wrong-id", secret, time.Now().Unix()), wrongID), ErrVideoFeeWebhookPayload)
}
