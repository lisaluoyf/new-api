package model

import (
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

func settlementFixture(t *testing.T) *TextSettlement {
	setupAtomicBillingTestDB(t)
	oldLog := LOG_DB
	LOG_DB = DB
	t.Cleanup(func() { LOG_DB = oldLog })
	require.NoError(t, DB.AutoMigrate(&TextSettlement{}, &Log{}, &Channel{}, &UserSubscription{}, &SubscriptionPreConsumeRecord{}))
	require.NoError(t, DB.Create(&User{Id: 1, Username: "settlement-user", Quota: 100}).Error)
	require.NoError(t, DB.Create(&Token{Id: 2, UserId: 1, Key: "settlement-key", RemainQuota: 100, UsedQuota: 10}).Error)
	require.NoError(t, DB.Create(&Channel{Id: 3}).Error)
	item := &TextSettlement{RequestId: "settlement-request", UserId: 1, TokenId: 2, ChannelId: 3, FundingSource: "wallet", PreConsumedQuota: 10, TokenConsumed: 10, Quota: 50}
	require.NoError(t, FreezeTextSettlement(item, &Log{UserId: 1, RequestId: item.RequestId, Type: LogTypeConsume, Quota: 50, PromptTokens: 12, CompletionTokens: 7, CreatedAt: common.GetTimestamp()}, AccountingLogFields{UserFinalAmountUSD: .0001, Snapshot: "frozen-prices"}))
	return item
}

func TestTextSettlementBalanceFailureRetriesOriginalLogOnce(t *testing.T) {
	item := settlementFixture(t)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", 1).Update("quota", 0).Error)
	result, err := ApplyTextSettlement(item.Id)
	require.ErrorIs(t, err, ErrSettlementBalance)
	require.Equal(t, "pending", result.Status)
	require.NoError(t, PublishTextSettlementLog(item.Id))
	var log Log
	require.NoError(t, DB.First(&log).Error)
	originalID := log.Id
	require.Equal(t, LogTypeError, log.Type)
	require.Zero(t, log.Quota)
	require.Equal(t, 12, log.PromptTokens)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", 1).Update("quota", 100).Error)
	for i := 0; i < 3; i++ {
		_, err = ApplyTextSettlement(item.Id)
		require.NoError(t, err)
	}
	var user User
	var token Token
	require.NoError(t, DB.First(&user, 1).Error)
	require.NoError(t, DB.First(&token, 2).Error)
	require.Equal(t, 60, user.Quota)
	require.Equal(t, 50, user.UsedQuota)
	require.Equal(t, 1, user.RequestCount)
	require.Equal(t, 60, token.RemainQuota)
	require.Equal(t, 50, token.UsedQuota)
	var logs []Log
	require.NoError(t, DB.Find(&logs).Error)
	require.Len(t, logs, 1)
	require.Equal(t, originalID, logs[0].Id)
	require.Equal(t, LogTypeConsume, logs[0].Type)
	require.Equal(t, 50, logs[0].Quota)
	require.Equal(t, "frozen-prices", logs[0].AccountingSnapshot)
}

func TestTextSettlementTokenFailureRollsBackWalletAndCounters(t *testing.T) {
	item := settlementFixture(t)
	require.NoError(t, DB.Model(&Token{}).Where("id = ?", 2).Update("remain_quota", 0).Error)
	_, err := ApplyTextSettlement(item.Id)
	require.ErrorIs(t, err, ErrSettlementBalance)
	var user User
	require.NoError(t, DB.First(&user, 1).Error)
	require.Equal(t, 100, user.Quota)
	require.Zero(t, user.RequestCount)
	require.Zero(t, user.UsedQuota)
}

func TestTextSettlementLogFailureRollsBackDebit(t *testing.T) {
	item := settlementFixture(t)
	require.NoError(t, DB.Migrator().DropTable(&Log{}))
	result, err := ApplyTextSettlement(item.Id)
	require.Error(t, err)
	require.Equal(t, "pending", result.Status)
	var user User
	require.NoError(t, DB.First(&user, 1).Error)
	require.Equal(t, 100, user.Quota)
	require.Zero(t, user.RequestCount)
	require.NoError(t, DB.AutoMigrate(&Log{}))
	_, err = ApplyTextSettlement(item.Id)
	require.NoError(t, err)
}

func TestTextSettlementSubscriptionAndTokenCommitTogether(t *testing.T) {
	item := settlementFixture(t)
	require.NoError(t, DB.Create(&UserSubscription{Id: 7, UserId: 1, AmountTotal: 200, AmountUsed: 10}).Error)
	require.NoError(t, DB.Create(&SubscriptionPreConsumeRecord{RequestId: item.RequestId, UserId: 1, UserSubscriptionId: 7, PreConsumed: 10}).Error)
	require.NoError(t, DB.Model(item).Updates(map[string]interface{}{"funding_source": "subscription", "subscription_id": 7}).Error)
	require.NoError(t, DB.Model(&Token{}).Where("id = ?", 2).Update("remain_quota", 0).Error)
	_, err := ApplyTextSettlement(item.Id)
	require.Error(t, err)
	var sub UserSubscription
	require.NoError(t, DB.First(&sub, 7).Error)
	require.EqualValues(t, 10, sub.AmountUsed)
	require.NoError(t, DB.Model(&Token{}).Where("id = ?", 2).Update("remain_quota", 100).Error)
	_, err = ApplyTextSettlement(item.Id)
	require.NoError(t, err)
	require.NoError(t, DB.First(&sub, 7).Error)
	require.EqualValues(t, 50, sub.AmountUsed)
}

func TestTextSettlementSeparateLogDBRetriesWithoutSecondDebit(t *testing.T) {
	item := settlementFixture(t)
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"-logs?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	LOG_DB = db
	result, err := ApplyTextSettlement(item.Id)
	require.Error(t, err, "missing separate log table must leave an outbox delivery")
	require.Equal(t, "settled", result.Status)
	require.NoError(t, db.AutoMigrate(&Log{}))
	for i := 0; i < 3; i++ {
		_, err = ApplyTextSettlement(item.Id)
		require.NoError(t, err)
	}
	var user User
	require.NoError(t, DB.First(&user, 1).Error)
	require.Equal(t, 60, user.Quota)
	require.Equal(t, 1, user.RequestCount)
	var logs []Log
	require.NoError(t, db.Find(&logs).Error)
	require.Len(t, logs, 1)
	require.Equal(t, 50, logs[0].Quota)
}

func TestTextSettlementRejectsChangedQuotaForSameRequest(t *testing.T) {
	item := settlementFixture(t)
	duplicate := *item
	duplicate.Id = 0
	duplicate.Quota = 60
	require.Error(t, FreezeTextSettlement(&duplicate, &Log{UserId: 1, RequestId: item.RequestId, Type: LogTypeConsume, Quota: 60}, AccountingLogFields{}))
	var stored TextSettlement
	require.NoError(t, DB.First(&stored, item.Id).Error)
	require.Equal(t, 50, stored.Quota)
}
