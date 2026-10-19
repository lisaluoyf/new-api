package service

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestOriginalVideoRefundSyncPersistsReceiptAndDoesNotReplayCredit(t *testing.T) {
	old, oldLog := model.DB, model.LOG_DB
	t.Cleanup(func() { model.DB, model.LOG_DB = old, oldLog })
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	model.DB, model.LOG_DB = db, db
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Token{}, &model.VideoSubmission{}, &model.VideoSubmissionRefund{}, &model.VideoRefundLogDelivery{}, &model.BillingHold{}, &model.Log{}))
	seedUser(t, 1, 960)
	seedToken(t, 2, 1, "retry-billing-key", 960)
	require.NoError(t, db.Model(&model.Token{}).Where("id = ?", 2).Update("used_quota", 40).Error)
	info := retryBillingInfo(1000)
	info.ForcePreConsume = true
	info.TaskRelayInfo = &relaycommon.TaskRelayInfo{PublicTaskID: "task_original_sync"}
	info.OriginModelName = "seedance-2.5"
	c := retryBillingContext()
	info.Billing = &BillingSession{relayInfo: info, funding: &WalletFunding{userId: 1, consumed: 40}, tokenConsumed: 40, preConsumedQuota: 40}
	snap := model.VideoSubmissionRecovery{Task: model.Task{TaskID: info.PublicTaskID, UserId: 1, Quota: 40}, PrivateData: model.TaskPrivateData{TokenId: 2, BillingSource: "wallet"}}
	raw, err := common.Marshal(snap)
	require.NoError(t, err)
	row := model.VideoSubmission{ID: "original-sync", UserID: 1, TaskID: info.PublicTaskID, RequestID: info.RequestId, Model: info.OriginModelName, Status: model.VideoSubmissionUnknown, RecoveryJSON: string(raw)}
	require.NoError(t, db.Create(&row).Error)
	require.NoError(t, info.Billing.RefundSync(c))
	require.NoError(t, info.Billing.RefundSync(c))
	// A second process/session still cannot credit the original refund twice.
	second := &BillingSession{relayInfo: info, funding: &WalletFunding{userId: 1, consumed: 40}, tokenConsumed: 40, preConsumedQuota: 40}
	require.NoError(t, second.RefundSync(c))
	var u model.User
	var token model.Token
	require.NoError(t, db.First(&u, 1).Error)
	require.NoError(t, db.First(&token, 2).Error)
	require.Equal(t, 1000, u.Quota)
	require.Equal(t, 1000, token.RemainQuota)
	require.Zero(t, token.UsedQuota)
	var count int64
	require.NoError(t, db.Model(&model.Log{}).Where("type = ?", model.LogTypeRefund).Count(&count).Error)
	require.EqualValues(t, 1, count)
	details, err := model.GetVideoSubmissionRefundDetails(1, info.PublicTaskID)
	require.NoError(t, err)
	require.Equal(t, false, details["new_submission_safe"])
}
