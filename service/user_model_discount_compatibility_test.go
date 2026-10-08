package service

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/stretchr/testify/require"
)

func TestUserModelDiscountAbsentKeepsTextDebitAndLog(t *testing.T) {
	for name, rules := range map[string]map[string]float64{
		"unset": nil, "empty": {}, "one": {"retry-billing-model": 1}, "other_model": {"another-model": .5},
	} {
		t.Run(name, func(t *testing.T) {
			truncate(t)
			seedUser(t, 1, 5000)
			seedToken(t, 2, 1, "retry-billing-key", 5000)
			seedChannel(t, 3)
			info := retryBillingInfo(5000)
			info.ChannelMeta = &relaycommon.ChannelMeta{ChannelId: 3}
			info.StartTime = time.Now()
			info.FirstResponseTime = info.StartTime
			info.ForcePreConsume = true
			info.UserSetting.ModelDiscountRatios = rules
			price := types.PriceData{ModelRatio: 1, CompletionRatio: 1, QuotaToPreConsume: 950, GroupRatioInfo: types.GroupRatioInfo{GroupRatio: .95, GroupSpecialRatio: -1}}
			info.SetWalletPriceData(price)
			ctx := retryBillingContext()
			ctx.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
			ctx.Set(common.RequestIdKey, info.RequestId)
			require.Nil(t, PreConsumeBilling(ctx, 950, info))
			require.Equal(t, price, info.PriceData)
			require.Equal(t, 950, info.Billing.GetPreConsumedQuota())
			require.NoError(t, PostTextConsumeQuota(ctx, info, &dto.Usage{PromptTokens: 1000, TotalTokens: 1000}, nil))
			var user model.User
			var token model.Token
			var log model.Log
			require.NoError(t, model.DB.First(&user, 1).Error)
			require.NoError(t, model.DB.First(&token, 2).Error)
			require.NoError(t, model.LOG_DB.Where("request_id = ? AND type = ?", info.RequestId, model.LogTypeConsume).First(&log).Error)
			require.Equal(t, 4050, user.Quota)
			require.Equal(t, 950, user.UsedQuota)
			require.Equal(t, 4050, token.RemainQuota)
			require.Equal(t, 950, token.UsedQuota)
			require.Equal(t, 950, log.Quota)
			other, err := common.StrToMap(log.Other)
			require.NoError(t, err)
			require.NotContains(t, other, "user_model_discount")
			require.InDelta(t, .95, other["group_ratio"], 1e-12)
		})
	}
}

func TestUserModelDiscountAbsentKeepsDirectWalletSnapshot(t *testing.T) {
	truncate(t)
	seedUser(t, 1, 5000)
	seedToken(t, 2, 1, "retry-billing-key", 5000)
	info := retryBillingInfo(5000)
	info.ForcePreConsume = true
	info.PriceData = types.PriceData{QuotaToPreConsume: 950, GroupRatioInfo: types.GroupRatioInfo{GroupRatio: .95}}
	snapshot := &billingexpr.BillingSnapshot{GroupRatio: .95, EstimatedQuotaAfterGroup: 950}
	info.TieredBillingSnapshot = snapshot
	ctx := retryBillingContext()
	require.Nil(t, PreConsumeBilling(ctx, 950, info))
	require.Same(t, snapshot, info.TieredBillingSnapshot)
	require.Nil(t, info.WalletPriceData)
	require.Equal(t, 950, info.Billing.GetPreConsumedQuota())
	require.NoError(t, info.Billing.RefundSync(ctx))
	require.NoError(t, info.Billing.RefundSync(ctx))
	require.Equal(t, 5000, getUserQuota(t, 1))
}

func TestUserModelDiscountAbsentKeepsQuotaExactly(t *testing.T) {
	largeQuota := int64(1<<53 + 1)
	quota := int(largeQuota)
	for _, rules := range []map[string]float64{nil, {}, {"model": 1}, {"other": .5}} {
		setting := dto.UserSetting{ModelDiscountRatios: rules}
		require.Equal(t, quota, ApplyUserModelDiscountToQuota(quota, setting, "model"))
		snapshot := &billingexpr.BillingSnapshot{GroupRatio: .95, EstimatedQuotaAfterGroup: 950}
		require.Same(t, snapshot, ApplyUserModelDiscountToBillingSnapshot(snapshot, setting, "model"))
	}
}

func TestUserModelDiscountAbsentKeepsAsyncSettlementRules(t *testing.T) {
	for _, discount := range []float64{0, 1, .9} {
		t.Run(common.GetJsonString(discount), func(t *testing.T) {
			truncate(t)
			seedUser(t, 1, 10000)
			seedToken(t, 2, 1, "retry-billing-key", 10000)
			seedChannel(t, 3)
			require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", 1).Update("used_quota", 90).Error)
			require.NoError(t, model.DB.Model(&model.Token{}).Where("id = ?", 2).Update("used_quota", 90).Error)
			task := makeTask(1, 3, 90, 2, BillingSourceWallet, 0)
			task.TaskID = "compatibility-async"
			task.PrivateData.BillingContext = &model.TaskBillingContext{OriginModelName: "gpt-4", ModelRatio: 1, GroupRatio: .8, UserModelDiscount: discount}
			modelRatio, exists, _ := ratio_setting.GetModelRatio("gpt-4")
			require.True(t, exists)
			groupRatio := ratio_setting.GetGroupRatio("default")
			if special, ok := ratio_setting.GetGroupGroupRatio("default", "default"); ok {
				groupRatio = special
			}
			expected := int(100 * modelRatio * groupRatio)
			if discount > 0 && discount < 1 {
				expected = 80
			}
			RecalculateTaskQuotaByTokens(context.Background(), task, 100)
			require.Equal(t, expected, task.Quota)
			require.Equal(t, 10000+90-expected, getUserQuota(t, 1))
			require.Equal(t, expected, getUserUsedQuota(t, 1))
			var token model.Token
			require.NoError(t, model.DB.First(&token, 2).Error)
			require.Equal(t, 10000+90-expected, token.RemainQuota)
			require.Equal(t, expected, token.UsedQuota)
		})
	}
}

func TestUserModelDiscountAbsentKeepsAsyncMissingGroupUnchanged(t *testing.T) {
	truncate(t)
	seedUser(t, 1, 10000)
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", 1).Update("group", "").Error)
	seedToken(t, 2, 1, "retry-billing-key", 10000)
	seedChannel(t, 3)
	task := makeTask(1, 3, 90, 2, BillingSourceWallet, 0)
	task.Group = ""
	task.PrivateData.BillingContext = &model.TaskBillingContext{OriginModelName: "gpt-4", ModelRatio: 1, GroupRatio: .8}
	RecalculateTaskQuotaByTokens(context.Background(), task, 100)
	require.Equal(t, 90, task.Quota)
	require.Equal(t, 10000, getUserQuota(t, 1))
}

func TestUserModelDiscountAbsentKeepsRealtimeLegacyPreConsume(t *testing.T) {
	oldDB := model.DB
	oldPath, oldMaster := common.SQLitePath, common.IsMasterNode
	oldSQLite, oldMySQL, oldPostgreSQL := common.UsingSQLite, common.UsingMySQL, common.UsingPostgreSQL
	t.Cleanup(func() {
		model.DB = oldDB
		common.SQLitePath, common.IsMasterNode = oldPath, oldMaster
		common.UsingSQLite, common.UsingMySQL, common.UsingPostgreSQL = oldSQLite, oldMySQL, oldPostgreSQL
	})
	t.Setenv("SQL_DSN", "local")
	t.Setenv("LOG_SQL_DSN", "")
	common.SQLitePath = filepath.Join(t.TempDir(), "realtime-compatibility.db")
	common.IsMasterNode = false
	common.UsingMySQL, common.UsingPostgreSQL = false, false
	require.NoError(t, model.InitDB())
	database, err := model.DB.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, database.Close()) })
	require.NoError(t, model.DB.AutoMigrate(&model.User{}, &model.Token{}, &model.TextSettlement{}))
	seedUser(t, 1, 10000)
	seedToken(t, 2, 1, "retry-billing-key", 10000)
	info := retryBillingInfo(10000)
	info.OriginModelName = "gpt-4"
	info.ForcePreConsume = true
	info.SetWalletPriceData(types.PriceData{ModelRatio: 1, QuotaToPreConsume: 950, GroupRatioInfo: types.GroupRatioInfo{GroupRatio: .8}})
	ctx := retryBillingContext()
	require.Nil(t, PreConsumeBilling(ctx, 950, info))
	usage := &dto.RealtimeUsage{InputTokens: 100, TotalTokens: 100}
	usage.InputTokenDetails.TextTokens = 100
	require.NoError(t, PreWssConsumeQuota(ctx, info, usage))
	modelRatio, _, _ := ratio_setting.GetModelRatio(info.OriginModelName)
	groupRatio := ratio_setting.GetGroupRatio(info.UsingGroup)
	if special, ok := ratio_setting.GetGroupGroupRatio(info.UserGroup, info.UsingGroup); ok {
		groupRatio = special
	}
	expected := int(100 * modelRatio * groupRatio)
	require.Equal(t, 950, info.Billing.GetPreConsumedQuota())
	require.Equal(t, 950, info.FinalPreConsumedQuota)
	require.Zero(t, info.RealtimeReservedQuota)
	var user model.User
	var token model.Token
	require.NoError(t, model.DB.First(&user, 1).Error)
	require.NoError(t, model.DB.First(&token, 2).Error)
	require.Equal(t, 10000-950-expected, user.Quota)
	require.Equal(t, 10000-950-expected, token.RemainQuota)
}
