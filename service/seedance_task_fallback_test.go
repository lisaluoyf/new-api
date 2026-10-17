package service

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func fallbackFixture(t *testing.T) (*gorm.DB, *model.Task) {
	db, _ := unifiedRoutingFixture(t)
	require.NoError(t, db.Create(&model.ChannelModelPricing{ChannelId: 8, ModelName: "seedance-2.0-fast", InputPrice: 0.05, GroupRatio: 1, PricingSource: "manual"}).Error)
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(&model.Task{}, &model.SeedanceTaskFallback{}, &model.VideoVerificationRun{}, &model.TaskWebhookEvent{}, &model.User{}, &model.Token{}, &model.Log{}, &model.ImageTaskRefund{}, &model.ImageRefundLogDelivery{}, &model.SeedanceBillingReceipt{}))
	oldLog := model.LOG_DB
	model.LOG_DB = db
	t.Cleanup(func() { model.LOG_DB = oldLog })
	old := PrepareSeedanceFallbackSubmit
	t.Cleanup(func() { PrepareSeedanceFallbackSubmit = old })
	PrepareSeedanceFallbackSubmit = func(c *gin.Context, task *model.Task, ch *model.Channel, key string) (func() (*SeedanceFallbackResult, error), error) {
		return func() (*SeedanceFallbackResult, error) {
			return &SeedanceFallbackResult{UpstreamID: "backup-upstream", Platform: constant.TaskPlatformApimartVideo, UpstreamModel: "seedance-2.0-fast", Data: []byte(`{"code":"success"}`)}, nil
		}, nil
	}
	task := &model.Task{TaskID: model.GenerateTaskID(), UserId: 1, ChannelId: 7, Status: model.TaskStatusSubmitted, Progress: "0%", SubmitTime: time.Now().Unix() - 90, Properties: model.Properties{OriginModelName: "seedance-2.0-fast"}, PrivateData: model.TaskPrivateData{UpstreamTaskID: "original-upstream", SeedanceRequest: map[string]any{"billing_variant": "480P"}, SeedanceFallback: &model.SeedanceFallbackRequest{TokenGroup: "default", Fields: map[string]any{"model": "seedance-2.0-fast", "prompt": "A paper kite", "duration": float64(5), "resolution": "480p", "image_urls": []any{"asset://logical-a", "asset://logical-b"}}}}}
	require.NoError(t, db.Create(task).Error)
	return db, task
}

func TestSeedanceAsyncFallbackAllowlist(t *testing.T) {
	for _, s := range []string{"Task submission failed; please check the request and try again later", "User has been banned", " USER HAS BEEN BANNED.\n", "Account has been banned", "Account has been disabled", "Invalid API key", "API key has expired", "API key has been revoked", "Service temporarily unavailable"} {
		require.True(t, seedanceAsyncFailureRetryable(s), s)
	}
	for _, s := range []string{"The output may contain sensitive information", "copyright restrictions", "Pixel count must be between 407696 and 8295044.", "Aspect ratio must be between 0.4 and 2.5.", "Task did not complete within 1 hour; held balance has been returned.", "upstream returned error", "HTTP timeout", "User has been banned for content policy violations", "Prompt contains: User has been banned", "Invalid request", "User has been banned; request timed out", "account unavailable"} {
		require.False(t, seedanceAsyncFailureRetryable(s), s)
	}
}

func TestSeedanceAsyncFallbackSameTaskOneSendNoSecondCharge(t *testing.T) {
	db, task := fallbackFixture(t)
	sends := 0
	prepare := PrepareSeedanceFallbackSubmit
	PrepareSeedanceFallbackSubmit = func(c *gin.Context, t *model.Task, ch *model.Channel, key string) (func() (*SeedanceFallbackResult, error), error) {
		send, e := prepare(c, t, ch, key)
		return func() (*SeedanceFallbackResult, error) { sends++; return send() }, e
	}
	won, err := beginSeedanceTaskFallback(context.Background(), task, model.TaskStatusSubmitted, "Task submission failed")
	require.NoError(t, err)
	require.True(t, won)
	var pending model.Task
	require.NoError(t, db.First(&pending, task.ID).Error)
	require.Equal(t, model.TaskStatus(model.TaskStatusRetrying), pending.Status)
	processSeedanceTaskFallbacks(context.Background())
	processSeedanceTaskFallbacks(context.Background())
	var fresh model.Task
	require.NoError(t, db.First(&fresh, task.ID).Error)
	require.Equal(t, 1, sends)
	require.Equal(t, task.TaskID, fresh.TaskID)
	require.Equal(t, 8, fresh.ChannelId)
	require.Equal(t, "backup-upstream", fresh.GetUpstreamTaskID())
	require.Equal(t, model.TaskStatus(model.TaskStatusSubmitted), fresh.Status)
	require.Equal(t, "generating_backup", fresh.PrivateData.SeedanceFallback.Stage)
	var count int64
	require.NoError(t, db.Model(&model.Log{}).Count(&count).Error)
	require.Zero(t, count)
	// Primary responses cannot overwrite a newly submitted backup, even when
	// both attempts have the same public task status.
	stale := *task
	stale.Status = model.TaskStatusFailure
	won, err = stale.UpdateWithStatusAndChannel(model.TaskStatusSubmitted, 7)
	require.NoError(t, err)
	require.False(t, won)
	var job model.SeedanceTaskFallback
	require.NoError(t, db.First(&job, task.ID).Error)
	require.Equal(t, "original-upstream", job.OriginalUpstreamID)
	require.Equal(t, "backup-upstream", job.UpstreamID)
}

func TestSeedanceAsyncFallbackWaitsForEveryApprovedCopy(t *testing.T) {
	db, task := fallbackFixture(t)
	require.NoError(t, db.Model(&model.SeedanceResource{}).Where("id = ?", "logical-b-account-b").Update("status", "Pending").Error)
	won, err := beginSeedanceTaskFallback(context.Background(), task, model.TaskStatusSubmitted, "Task submission failed")
	require.NoError(t, err)
	require.True(t, won)
	processSeedanceTaskFallbacks(context.Background())
	var fresh model.Task
	require.NoError(t, db.First(&fresh, task.ID).Error)
	require.Equal(t, model.TaskStatus(model.TaskStatusRetrying), fresh.Status)
	require.NoError(t, db.Model(&model.SeedanceResource{}).Where("id = ?", "logical-b-account-b").Update("status", "Active").Error)
	require.NoError(t, db.Model(&model.SeedanceTaskFallback{}).Where("task_id = ?", task.ID).Update("lease_until", 0).Error)
	processSeedanceTaskFallbacks(context.Background())
	require.NoError(t, db.First(&fresh, task.ID).Error)
	require.Equal(t, model.TaskStatus(model.TaskStatusSubmitted), fresh.Status)
}

func TestSeedanceAsyncFallbackReviewRejectionRefundsOnce(t *testing.T) {
	db, task := fallbackFixture(t)
	task.Quota = 500
	task.PrivateData.BillingSource = "wallet"
	require.NoError(t, db.Save(task).Error)
	require.NoError(t, db.Create(&model.User{Id: 1, Username: "recovery-test", Quota: 1000, UsedQuota: 500}).Error)
	require.NoError(t, db.Create(&model.Log{UserId: 1, Type: model.LogTypeConsume, Quota: 500, ChannelId: 7, ModelName: "seedance-2.0-fast", Other: common.MapToJsonStr(map[string]any{"task_id": task.TaskID})}).Error)
	won, err := beginSeedanceTaskFallback(context.Background(), task, model.TaskStatusSubmitted, "Task submission failed")
	require.NoError(t, err)
	require.True(t, won)
	require.NoError(t, db.Model(&model.SeedanceResource{}).Where("id = ?", "logical-b-account-b").Update("status", "Failed").Error)
	processSeedanceTaskFallbacks(context.Background())
	var fresh model.Task
	require.NoError(t, db.First(&fresh, task.ID).Error)
	require.Equal(t, model.TaskStatus(model.TaskStatusFailure), fresh.Status)
	require.Contains(t, fresh.FailReason, "review was rejected")
	require.NoError(t, refundSeedanceFallbackTask(&fresh, fresh.FailReason))
	var user model.User
	require.NoError(t, db.First(&user, 1).Error)
	require.Equal(t, 1500, user.Quota)
	require.Zero(t, user.UsedQuota)
	var count int64
	require.NoError(t, db.Model(&model.Log{}).Where("type = ?", model.LogTypeRefund).Count(&count).Error)
	require.EqualValues(t, 1, count)
}

func TestSeedanceAsyncFallbackCrashedSenderNeverResubmits(t *testing.T) {
	db, task := fallbackFixture(t)
	won, err := beginSeedanceTaskFallback(context.Background(), task, model.TaskStatusSubmitted, "Task submission failed")
	require.NoError(t, err)
	require.True(t, won)
	require.NoError(t, db.Model(&model.SeedanceTaskFallback{}).Where("task_id = ?", task.ID).Updates(map[string]any{"state": "submitting", "deadline": time.Now().Unix() - 1}).Error)
	sends := 0
	PrepareSeedanceFallbackSubmit = func(*gin.Context, *model.Task, *model.Channel, string) (func() (*SeedanceFallbackResult, error), error) {
		sends++
		return nil, fmt.Errorf("must not send")
	}
	processSeedanceTaskFallbacks(context.Background())
	var fresh model.Task
	require.NoError(t, db.First(&fresh, task.ID).Error)
	require.Equal(t, model.TaskStatus(model.TaskStatusFailure), fresh.Status)
	require.Zero(t, sends)
}

func TestSeedanceAsyncFallbackUploadsThenWaitsForTargetReview(t *testing.T) {
	uploads, polls := 0, 0
	db := bytePlusTestLibrary(t, func(w http.ResponseWriter, r *http.Request) {
		action, _ := bytePlusTestRequest(t, r)
		switch action {
		case "CreateAssetGroup":
			fmt.Fprint(w, `{"Result":{"Id":"target-group"}}`)
		case "CreateAsset":
			uploads++
			fmt.Fprint(w, `{"Result":{"Id":"target-asset"}}`)
		case "GetAsset":
			polls++
			status := "Processing"
			if polls > 1 {
				status = "Active"
			}
			fmt.Fprintf(w, `{"Result":{"Status":%q}}`, status)
		default:
			t.Fatalf("Unexpected action %s", action)
		}
	})
	require.NoError(t, db.Create(&model.SeedanceResource{ID: "logical-group", UserID: 1, Kind: "group", GroupType: unifiedSeedanceKind, Status: "Active"}).Error)
	require.NoError(t, db.Create(&model.SeedanceResource{ID: "logical-asset", UserID: 1, Kind: "asset", GroupType: unifiedSeedanceKind, GroupID: "logical-group", SourceURL: "https://example.com/kite.png", AssetType: "Image", Status: "Active"}).Error)
	task := &model.Task{TaskID: "task-recovery-review", UserId: 1, Properties: model.Properties{OriginModelName: "seedance-2.5"}, PrivateData: model.TaskPrivateData{SeedanceFallback: &model.SeedanceFallbackRequest{Fields: map[string]any{"model": "seedance-2.5", "image_urls": []any{"asset://logical-asset"}}}}}
	ch, err := model.GetChannelById(283, true)
	require.NoError(t, err)
	c := seedanceContext(1)
	ready, _, err := prepareSeedanceFallbackMaterials(c, task, &model.SeedanceTaskFallback{}, ch)
	require.NoError(t, err)
	require.False(t, ready)
	require.Equal(t, 1, uploads)
	c = seedanceContext(1)
	ready, key, err := prepareSeedanceFallbackMaterials(c, task, &model.SeedanceTaskFallback{}, ch)
	require.NoError(t, err)
	require.True(t, ready)
	require.Equal(t, "inference-key", key)
	require.Equal(t, 1, uploads)
	fields, err := SeedanceRequestFields(c)
	require.NoError(t, err)
	resolved, err := ResolveSeedanceAssetReferences(c, fields, 283, key)
	require.NoError(t, err)
	require.Contains(t, fmt.Sprint(resolved), "asset://target-asset")
	require.NotContains(t, fmt.Sprint(fields), "target-asset")
}

func TestSeedanceAsyncFallbackCaptureDoesNotOverrideExplicitChannel(t *testing.T) {
	_, c := unifiedRoutingFixture(t)
	task := &model.Task{Properties: model.Properties{OriginModelName: "seedance-2.0-fast"}}
	c.Set("seedance_user_pinned_channel", true)
	CaptureSeedanceFallbackRequest(c, task)
	require.Nil(t, task.PrivateData.SeedanceFallback)
	c.Set("seedance_user_pinned_channel", false)
	CaptureSeedanceFallbackRequest(c, task)
	require.NotNil(t, task.PrivateData.SeedanceFallback)
	raw, err := common.Marshal(SeedanceCompatibleTaskResponse(&model.Task{TaskID: "public", Status: model.TaskStatusRetrying, Properties: task.Properties, PrivateData: model.TaskPrivateData{SeedanceFallback: &model.SeedanceFallbackRequest{Started: true, Stage: "preparing_materials", Fields: map[string]any{"prompt": "private-prompt"}}}}))
	require.NoError(t, err)
	require.False(t, strings.Contains(string(raw), "private-prompt"))
	require.Contains(t, string(raw), "preparing_materials")
}

func TestSeedanceAsyncFallbackConcurrentPollerCannotRepeatPOST(t *testing.T) {
	db, task := fallbackFixture(t)
	won, err := beginSeedanceTaskFallback(context.Background(), task, model.TaskStatusSubmitted, "Task submission failed")
	require.NoError(t, err)
	require.True(t, won)
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var sends atomic.Int32
	PrepareSeedanceFallbackSubmit = func(*gin.Context, *model.Task, *model.Channel, string) (func() (*SeedanceFallbackResult, error), error) {
		return func() (*SeedanceFallbackResult, error) {
			sends.Add(1)
			close(entered)
			<-release
			return &SeedanceFallbackResult{UpstreamID: "only-backup", Platform: constant.TaskPlatformApimartVideo, UpstreamModel: "seedance-2.0-fast", Data: []byte(`{}`)}, nil
		}, nil
	}
	go func() { processSeedanceTaskFallbacks(context.Background()); close(done) }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("Sender did not reach POST")
	}
	processSeedanceTaskFallbacks(context.Background())
	close(release)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Sender did not finish")
	}
	require.EqualValues(t, 1, sends.Load())
	var fresh model.Task
	require.NoError(t, db.First(&fresh, task.ID).Error)
	require.Equal(t, "only-backup", fresh.GetUpstreamTaskID())
}

func TestSeedanceAsyncFallbackAccountingKeepsUserTariffAndUsesTargetCost(t *testing.T) {
	db, task := fallbackFixture(t)
	task.Status = model.TaskStatusSuccess
	task.ChannelId = 8
	task.PrivateData.SeedanceFallback.Started = true
	require.NoError(t, db.Create(&model.SeedanceTaskFallback{TaskID: task.ID, State: "accepted", OriginalChannelID: 7, TargetChannelID: 8, TargetUnitCost: .02}).Error)
	snapshot := `{"channel_id":7,"prices":{"channel_cost":{"input_price":9},"channel_user":{"input_price":0.1}},"amounts_usd":{"channel_cost":9,"user_final":0.1},"units":{"duration_seconds":1}}`
	require.NoError(t, db.Create(&model.Log{UserId: 1, Type: model.LogTypeConsume, ChannelId: 7, ModelName: "seedance-2.0-fast", Quota: 500, AccountingStatus: "ok", AccountingSnapshot: snapshot, Other: common.MapToJsonStr(map[string]any{"task_id": task.TaskID})}).Error)
	require.NoError(t, backfillSeedanceFallbackAccounting(task))
	require.NoError(t, backfillSeedanceEstimatedCost(task, 10))
	row, err := model.FindConsumeLogRowForTask(1, task.TaskID)
	require.NoError(t, err)
	require.Equal(t, 8, row.ChannelId)
	require.Equal(t, 500, row.Quota)
	require.InDelta(t, .2, row.AccountingChannelCostAmountUSD, 1e-9)
	var got map[string]any
	require.NoError(t, common.UnmarshalJsonStr(row.AccountingSnapshot, &got))
	prices := got["prices"].(map[string]any)
	require.Equal(t, .1, prices["channel_user"].(map[string]any)["input_price"])
	require.Equal(t, .02, prices["channel_cost"].(map[string]any)["input_price"])
}
