package model

import (
	"fmt"
	"github.com/glebarez/sqlite"
	"github.com/tidwall/gjson"
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
	// Large JSON numbers must still identify the original pending log exactly.
	require.NoError(t, DB.Model(&TextSettlement{}).Where("id = ?", item.Id).Update("id", 7000001).Error)
	item.Id = 7000001
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

func freeCreditSettlementFixture(t *testing.T, planType string, used int64) *TextSettlement {
	item := settlementFixture(t)
	require.NoError(t, DB.AutoMigrate(&SubscriptionPlan{}))
	require.NoError(t, DB.Create(&SubscriptionPlan{Id: 8, Title: "free credits", PlanType: planType, PriceAmount: 0}).Error)
	require.NoError(t, DB.Create(&UserSubscription{Id: 7, UserId: 1, PlanId: 8, AmountTotal: 30, AmountUsed: used}).Error)
	require.NoError(t, DB.Create(&SubscriptionPreConsumeRecord{RequestId: item.RequestId, UserId: 1, UserSubscriptionId: 7, PreConsumed: 10}).Error)
	require.NoError(t, DB.Model(item).Updates(map[string]interface{}{"funding_source": "subscription", "subscription_id": 7}).Error)
	return item
}

func TestTextSettlementFreeCreditsUseWalletOnlyForUncoveredUsage(t *testing.T) {
	for _, planType := range []string{SubscriptionPlanTypeGPTTrial, SubscriptionPlanTypeGPTReferralReward} {
		for _, used := range []int64{10, 30} {
			t.Run(planType+fmt.Sprint(used), func(t *testing.T) {
				item := freeCreditSettlementFixture(t, planType, used)
				require.NoError(t, PublishTextSettlementLog(item.Id))
				var before Log
				require.NoError(t, DB.First(&before).Error)
				walletPart := int(used) + 10
				for i := 0; i < 3; i++ {
					result, err := ApplyTextSettlement(item.Id)
					require.NoError(t, err)
					require.Equal(t, "settled", result.Status)
					require.Equal(t, walletPart, result.WalletSupplementQuota)
				}
				var user User
				var token Token
				var sub UserSubscription
				var reservation SubscriptionPreConsumeRecord
				var logs []Log
				require.NoError(t, DB.First(&user, 1).Error)
				require.NoError(t, DB.First(&token, 2).Error)
				require.NoError(t, DB.First(&sub, 7).Error)
				require.NoError(t, DB.First(&reservation).Error)
				require.NoError(t, DB.Find(&logs).Error)
				require.Equal(t, 100-walletPart, user.Quota)
				require.Equal(t, 50, user.UsedQuota)
				require.Equal(t, 1, user.RequestCount)
				require.Equal(t, 60, token.RemainQuota)
				require.Equal(t, 50, token.UsedQuota)
				require.EqualValues(t, 30, sub.AmountUsed)
				require.EqualValues(t, 50-walletPart, reservation.PreConsumed)
				require.Len(t, logs, 1)
				require.Equal(t, before.Id, logs[0].Id)
				require.Equal(t, LogTypeConsume, logs[0].Type)
				require.Equal(t, "frozen-prices", logs[0].AccountingSnapshot)
				require.Equal(t, walletPart, logs[0].WalletSupplementQuota)
				require.Equal(t, int64(walletPart), gjson.Get(logs[0].Other, "wallet_supplement_quota").Int())
				require.Equal(t, int64(50-walletPart), gjson.Get(logs[0].Other, "subscription_settled_quota").Int())
				pending, err := HasPendingTextSettlement(1)
				require.NoError(t, err)
				require.False(t, pending)
			})
		}
	}
}

func TestTextSettlementWalletSupplementRollsBackOnFailure(t *testing.T) {
	for _, failure := range []string{"wallet", "token", "log", "reservation"} {
		t.Run(failure, func(t *testing.T) {
			item := freeCreditSettlementFixture(t, SubscriptionPlanTypeGPTReferralReward, 10)
			switch failure {
			case "wallet":
				require.NoError(t, DB.Model(&User{}).Where("id = ?", 1).Update("quota", 19).Error)
			case "token":
				require.NoError(t, DB.Model(&Token{}).Where("id = ?", 2).Update("remain_quota", 0).Error)
			case "log":
				require.NoError(t, DB.Migrator().DropTable(&Log{}))
			case "reservation":
				require.NoError(t, DB.Where("request_id = ?", item.RequestId).Delete(&SubscriptionPreConsumeRecord{}).Error)
			}
			result, err := ApplyTextSettlement(item.Id)
			require.Error(t, err)
			require.Equal(t, "pending", result.Status)
			require.Zero(t, result.WalletSupplementQuota)
			var user User
			var sub UserSubscription
			require.NoError(t, DB.First(&user, 1).Error)
			require.NoError(t, DB.First(&sub, 7).Error)
			expectedWallet := 100
			if failure == "wallet" {
				expectedWallet = 19
			}
			require.Equal(t, expectedWallet, user.Quota)
			require.Zero(t, user.UsedQuota)
			require.Zero(t, user.RequestCount)
			require.EqualValues(t, 10, sub.AmountUsed)
		})
	}
}

func TestTextSettlementDoesNotSpendWalletForPaidOrSubscriptionOnly(t *testing.T) {
	for _, kind := range []string{"paid", "subscription_only", "paid_snapshot"} {
		t.Run(kind, func(t *testing.T) {
			planType := SubscriptionPlanTypeGPTReferralReward
			if kind == "paid" {
				planType = SubscriptionPlanTypeGPTSubscription
			}
			item := freeCreditSettlementFixture(t, planType, 10)
			if kind == "subscription_only" {
				require.NoError(t, DB.Model(&User{}).Where("id = ?", 1).Update("setting", `{"billing_preference":"subscription_only"}`).Error)
			}
			if kind == "paid_snapshot" {
				require.NoError(t, DB.Model(&UserSubscription{}).Where("id = ?", 7).Update("paid_amount_snapshot", 1).Error)
			}
			_, err := ApplyTextSettlement(item.Id)
			require.ErrorIs(t, err, ErrSettlementBalance)
			var user User
			require.NoError(t, DB.First(&user, 1).Error)
			require.Equal(t, 100, user.Quota)
		})
	}
}

func TestResolvePendingTextSettlementsUsesAvailableWalletImmediately(t *testing.T) {
	item := freeCreditSettlementFixture(t, SubscriptionPlanTypeGPTReferralReward, 10)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", 1).Update("quota", 0).Error)
	pending, err := ResolvePendingTextSettlements(1)
	require.NoError(t, err)
	require.True(t, pending)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", 1).Update("quota", 100).Error)
	pending, err = ResolvePendingTextSettlements(1)
	require.NoError(t, err)
	require.False(t, pending)
	var stored TextSettlement
	require.NoError(t, DB.First(&stored, item.Id).Error)
	require.Equal(t, "settled", stored.Status)
	var user User
	require.NoError(t, DB.First(&user, 1).Error)
	require.Equal(t, 80, user.Quota)
}
