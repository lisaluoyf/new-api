package service

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupCancellationObservationTest(t *testing.T) {
	t.Helper()
	truncate(t)
	require.NoError(t, model.DB.AutoMigrate(&model.CancellationObservation{}, &model.CancellationObservationCursor{}))
	t.Cleanup(func() {
		model.DB.Exec("DELETE FROM cancellation_observations")
		model.DB.Exec("DELETE FROM cancellation_observation_cursors")
	})
}

func openObservationLogDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, db.AutoMigrate(&model.Log{}))
	return db
}

func TestCancellationObservationFrozenSnapshotAndConcurrentDuplicates(t *testing.T) {
	setupCancellationObservationTest(t)
	seedUser(t, 1, 10000)
	seedToken(t, 2, 1, "customer-secret", 10000)
	c := retryBillingContext()
	c.Set("channel_id", 97)
	c.Set("use_channel", []string{"97"})
	info := &relaycommon.RelayInfo{
		RequestId: "cancel-duplicate", UserId: 1, TokenId: 2, OriginModelName: "glm-5.3-flash",
		StartTime: time.Now().Add(-8 * time.Second), BillingSource: "subscription", SubscriptionId: 77,
		SubscriptionCycleId: 8, PriceData: types.PriceData{ModelRatio: 0.25, CacheRatio: 0},
		ObservationUpstreamRequestID: "provider-request-123",
		ChannelMeta:                  &relaycommon.ChannelMeta{ChannelId: 97, ApiKey: "supplier-secret", ChannelMultiKeyIndex: 3},
	}
	require.NoError(t, ObserveCanceledRelay(c, info))
	original, err := model.GetCancellationObservation(info.RequestId)
	require.NoError(t, err)
	require.Len(t, original.CredentialFingerprint, 64)
	require.Equal(t, "provider-request-123", original.UpstreamRequestId)
	require.NotContains(t, original.RequestSnapshot, "supplier-secret")
	require.NotContains(t, original.RequestSnapshot, "customer-secret")
	require.Contains(t, original.RequestSnapshot, `"subscription_id":77`)
	require.Contains(t, original.RequestSnapshot, `"CacheRatio":0`)
	info.PriceData.ModelRatio = 100
	info.SubscriptionId = 999
	// Duplicate live events, historical backfill, and an identity collision
	// cannot overwrite the original request's owner or request-time price.
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- model.CreateCancellationObservation(&model.CancellationObservation{
				RequestId: info.RequestId, UserId: 99, TokenId: 99, ChannelId: 245, Source: "audit_backfill",
			})
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	require.NoError(t, ObserveCanceledRelay(c, info))
	after, err := model.GetCancellationObservation(info.RequestId)
	require.NoError(t, err)
	require.Equal(t, original.RequestSnapshot, after.RequestSnapshot)
	require.Equal(t, original.UserId, after.UserId)
	var count int64
	require.NoError(t, model.DB.Model(&model.CancellationObservation{}).Count(&count).Error)
	require.EqualValues(t, 1, count)
	assertObservationBalances(t, 10000)
}

func assertObservationBalances(t *testing.T, want int) {
	t.Helper()
	var user model.User
	var token model.Token
	require.NoError(t, model.DB.First(&user, 1).Error)
	require.NoError(t, model.DB.First(&token, 2).Error)
	require.Equal(t, want, user.Quota)
	require.Equal(t, want, token.RemainQuota)
}

func TestCancellationObservationRefreshSeesLateSettlementRefundAndWrongOwner(t *testing.T) {
	for _, scenario := range []struct {
		name  string
		logs  []model.Log
		hold  string
		state string
	}{
		{"unknown", nil, "", "no_settlement_observed"},
		{"settled", []model.Log{{UserId: 1, TokenId: 2, Type: 2, Quota: 41}}, "", "consume_log_observed"},
		{"refunded", []model.Log{{UserId: 1, TokenId: 2, Type: 6, Quota: 100}}, "", "refund_observed"},
		{"both", []model.Log{{UserId: 1, TokenId: 2, Type: 2}, {UserId: 1, TokenId: 2, Type: 6}}, "", "manual_review"},
		{"duplicate_charge", []model.Log{{UserId: 1, TokenId: 2, Type: 2}, {UserId: 1, TokenId: 2, Type: 2}}, "", "manual_review"},
		{"wrong_user", []model.Log{{UserId: 99, TokenId: 2, Type: 2}}, "", "manual_review"},
		{"wrong_token", []model.Log{{UserId: 1, TokenId: 99, Type: 2}}, "", "manual_review"},
		{"hedge_loser", []model.Log{{UserId: 0, TokenId: 0, Type: 2}}, "", "no_settlement_observed"},
		{"pending_hold", nil, "pending", "hold_observed"},
		{"refunded_hold", nil, "refunded", "refund_observed"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			setupCancellationObservationTest(t)
			seedUser(t, 1, 10000)
			seedToken(t, 2, 1, "observation-key", 10000)
			now := common.GetTimestamp()
			item := &model.CancellationObservation{RequestId: "cancel-late", UserId: 1, TokenId: 2, ChannelId: 97, CreatedAt: now}
			require.NoError(t, model.CreateCancellationObservation(item))
			require.NoError(t, refreshCancellationObservation(*item, now))
			for _, row := range scenario.logs {
				row.RequestId = item.RequestId
				require.NoError(t, model.LOG_DB.Create(&row).Error)
			}
			if scenario.hold != "" {
				require.NoError(t, model.DB.Create(&model.BillingHold{RequestId: item.RequestId, UserId: 1, TokenId: 2, PreConsumedQuota: 777, Status: scenario.hold}).Error)
			}
			// A fresh worker after restart reads persisted state and late logs.
			for i := 1; i <= 3; i++ {
				fresh, err := model.GetCancellationObservation(item.RequestId)
				require.NoError(t, err)
				require.NoError(t, refreshCancellationObservation(*fresh, now+int64(i*301)))
			}
			after, err := model.GetCancellationObservation(item.RequestId)
			require.NoError(t, err)
			require.Equal(t, scenario.state, after.LocalState)
			require.Contains(t, after.LocalEvidence, `"proposed_customer_charge":null`)
			require.Contains(t, after.LocalEvidence, `"supplier_cost":null`)
			assertObservationBalances(t, 10000)
			var count int64
			require.NoError(t, model.LOG_DB.Model(&model.Log{}).Count(&count).Error)
			require.EqualValues(t, len(scenario.logs), count, "observer must not create financial logs")
		})
	}
}

func TestCancellationObservationBackfillSeparateLogDatabaseAndRetry(t *testing.T) {
	setupCancellationObservationTest(t)
	// Keep this test independent of LOG_DB/DB co-location: the observation
	// table exists only in DB. The other integration tests use co-located logs.
	oldLogDB := model.LOG_DB
	logDB := openObservationLogDB(t)
	model.LOG_DB = logDB
	t.Cleanup(func() { model.LOG_DB = oldLogDB })
	now := common.GetTimestamp()
	rows := []model.Log{
		{RequestId: "historical", UserId: 1, TokenId: 2, ChannelId: 97, ModelName: "glm-5.3-flash", CreatedAt: now - 100, Type: 5, Other: `{"error_code":"client_canceled","duration_ms":7990}`},
		{RequestId: "not-cancel", UserId: 1, ChannelId: 97, CreatedAt: now - 99, Type: 5, Other: `{"error_code":"bad_response"}`},
		{RequestId: "system-loser", UserId: 0, ChannelId: 97, CreatedAt: now - 98, Type: 5, Other: `{"error_code":"client_canceled"}`},
		{RequestId: "old", UserId: 1, ChannelId: 97, CreatedAt: now - 90000, Type: 5, Other: `{"error_code":"client_canceled"}`},
	}
	for i := range rows {
		require.NoError(t, logDB.Create(&rows[i]).Error)
	}
	for i := 0; i < 3; i++ {
		require.NoError(t, RunCancellationObservationOnce(now+int64(i)))
	}
	item, err := model.GetCancellationObservation("historical")
	require.NoError(t, err)
	require.Equal(t, "audit_backfill", item.Source)
	require.Equal(t, "exact_supplier_link_missing", item.ReviewReason)
	require.Contains(t, item.RequestSnapshot, `"historical_snapshot_missing":true`)
	var count int64
	require.NoError(t, model.DB.Model(&model.CancellationObservation{}).Count(&count).Error)
	require.EqualValues(t, 1, count)
	// Simulate a cancellation log committed after a prior cursor scan. A
	// subsequent cycle must discover it without duplicating historical rows.
	late := rows[0]
	late.Id = 0
	late.RequestId = "late-insert"
	require.NoError(t, logDB.Create(&late).Error)
	for i := 0; i < 3; i++ {
		require.NoError(t, RunCancellationObservationOnce(now+10+int64(i)))
	}
	_, err = model.GetCancellationObservation("late-insert")
	require.NoError(t, err)
}

func TestCancellationObservationConcurrentRefundAndWorker(t *testing.T) {
	setupCancellationObservationTest(t)
	seedUser(t, 1, 10000)
	seedToken(t, 2, 1, "retry-billing-key", 10000)
	info := retryBillingInfo(10000)
	info.ForcePreConsume = true
	c := retryBillingContext()
	c.Set("channel_id", 97)
	require.Nil(t, PreConsumeBilling(c, 300, info))
	require.NoError(t, ObserveCanceledRelay(c, info))
	item, err := model.GetCancellationObservation(info.RequestId)
	require.NoError(t, err)
	errs := make(chan error, 2)
	go func() { errs <- info.Billing.RefundSync(c) }()
	go func() { errs <- refreshCancellationObservation(*item, common.GetTimestamp()) }()
	for i := 0; i < 2; i++ {
		require.NoError(t, <-errs)
	}
	assertObservationBalances(t, 10000)
	var count int64
	require.NoError(t, model.DB.Model(&model.BillingHold{}).Count(&count).Error)
	require.Zero(t, count, fmt.Sprint("observation must not create financial holds"))
}

func TestCancellationObservationStaleWorkerCannotOverwriteFreshEvidence(t *testing.T) {
	setupCancellationObservationTest(t)
	now := common.GetTimestamp()
	item := &model.CancellationObservation{RequestId: "stale-worker", UserId: 1, TokenId: 2, ChannelId: 97, CreatedAt: now}
	require.NoError(t, model.CreateCancellationObservation(item))
	require.NoError(t, model.UpdateCancellationObservation(*item, "consume_log_observed", `{}`, "existing_consume_do_not_charge", now))
	require.NoError(t, model.UpdateCancellationObservation(*item, "no_settlement_observed", `{}`, "exact_supplier_link_missing", now))
	after, err := model.GetCancellationObservation(item.RequestId)
	require.NoError(t, err)
	require.Equal(t, "consume_log_observed", after.LocalState)
	require.EqualValues(t, 1, after.Revision)
}
