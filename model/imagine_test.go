package model

import (
	"fmt"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

func setupImagineTest(t *testing.T, repeat int, upstreamIDs []string) (*ImagineBatch, []ImagineTask) {
	t.Helper()
	truncateTables(t)
	require.NoError(t, DB.AutoMigrate(&ImagineBatch{}, &ImagineTask{}, &ImagineBillingEvent{}, &ImagineLogDelivery{}, &SubscriptionPreConsumeRecord{}))
	for _, table := range []any{&ImagineBatch{}, &ImagineTask{}, &ImagineBillingEvent{}, &ImagineLogDelivery{}} {
		require.NoError(t, DB.Where("1 = 1").Delete(table).Error)
	}
	user := User{Username: "imagine-test", Quota: 1000000 - repeat*22520}
	require.NoError(t, DB.Create(&user).Error)
	token := Token{UserId: user.Id, Key: "imagine-test-key", RemainQuota: 1000000 - repeat*22520, UsedQuota: repeat * 22520}
	require.NoError(t, DB.Create(&token).Error)
	channel := Channel{Name: "imagine-test"}
	require.NoError(t, DB.Create(&channel).Error)
	batch := &ImagineBatch{ID: "batch-test", RequestID: "imagine-request", CallbackToken: "secret", UserID: user.Id, TokenID: token.Id, ChannelID: channel.Id, Model: "midjourney-v8.2", Speed: "relax", Repeat: repeat, Status: "submitting", BaseUnitPrice: 0.04504, UnitQuota: 22520, ReservedQuota: repeat * 22520, BillingSource: "wallet"}
	require.NoError(t, DB.Create(batch).Error)
	require.NoError(t, AttachImagineTasks(batch.ID, upstreamIDs))
	var tasks []ImagineTask
	require.NoError(t, DB.Where("batch_id = ?", batch.ID).Order("id").Find(&tasks).Error)
	return batch, tasks
}

func TestImagineLogsPreserveRequestDataWithoutCallbackSecrets(t *testing.T) {
	for _, name := range []string{"midjourney-v8.2", "midjourney-niji-7"} {
		for _, status := range []string{"completed", "failed"} {
			t.Run(name+"/"+status, func(t *testing.T) {
				batch, tasks := setupImagineTest(t, 1, []string{"upstream-one"})
				request := map[string]any{"model": name, "prompt": "A blue teapot", "speed": "fast", "size": "16:9", "seed": 0, "raw": false,
					"image_urls": []string{"https://example.com/reference.png"}, "webhook": "https://example.com/callback-secret"}
				body, err := common.Marshal(request)
				require.NoError(t, err)
				require.NoError(t, DB.Model(batch).Updates(map[string]any{"model": name, "request_data": string(body)}).Error)
				require.NoError(t, ApplyImagineResult(tasks[0].ID, ImagineTask{Status: status, Images: `["https://example.com/result.png"]`}))
				var event ImagineBillingEvent
				require.NoError(t, DB.First(&event, "id = ?", tasks[0].ID+":"+status).Error)
				require.NotContains(t, event.LogData, "callback-secret")
				require.NoError(t, DeliverImagineBillingEvents())
				var row Log
				require.NoError(t, LOG_DB.Where("request_id = ?", batch.RequestID).First(&row).Error)
				var other map[string]any
				require.NoError(t, common.UnmarshalJsonStr(row.Other, &other))
				require.Contains(t, other, "admin_info")
				formatUserLogs([]*Log{&row}, 0)
				other = nil
				require.NoError(t, common.UnmarshalJsonStr(row.Other, &other))
				require.NotContains(t, other, "admin_info")
				delete(request, "webhook")
				expected, err := common.Marshal(request)
				require.NoError(t, err)
				actual, err := common.Marshal(other["request_data"])
				require.NoError(t, err)
				require.JSONEq(t, string(expected), string(actual))
			})
		}
	}
}

func TestImagineInvalidStoredRequestDataDoesNotBlockSettlement(t *testing.T) {
	for _, body := range []string{"", "null", "{}", "[]", "invalid", `{"webhook":"secret"}`} {
		t.Run(body, func(t *testing.T) {
			batch, tasks := setupImagineTest(t, 1, []string{"upstream-one"})
			require.NoError(t, DB.Model(batch).Update("request_data", body).Error)
			require.NoError(t, ApplyImagineResult(tasks[0].ID, ImagineTask{Status: "completed"}))
			var event ImagineBillingEvent
			require.NoError(t, DB.First(&event, "id = ?", tasks[0].ID+":completed").Error)
			var row Log
			require.NoError(t, common.UnmarshalJsonStr(event.LogData, &row))
			require.NotContains(t, row.Other, "request_data")
		})
	}
}

func TestImagineFourImagesAreOneTaskAndConcurrentSettlementIsIdempotent(t *testing.T) {
	batch, tasks := setupImagineTest(t, 4, []string{"upstream-one"})
	var user User
	require.NoError(t, DB.First(&user, batch.UserID).Error)
	require.Equal(t, 1000000-22520, user.Quota)
	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- ApplyImagineResult(tasks[0].ID, ImagineTask{Status: "completed", Images: `["https://a.test/1","https://a.test/2","https://a.test/3","https://a.test/4"]`})
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	require.NoError(t, DB.First(&user, batch.UserID).Error)
	require.Equal(t, 22520, user.UsedQuota)
	require.Equal(t, 1, user.RequestCount)
	require.NoError(t, DeliverImagineBillingEvents())
	require.NoError(t, DeliverImagineBillingEvents())
	var count int64
	require.NoError(t, LOG_DB.Model(&Log{}).Where("request_id = ?", batch.RequestID).Count(&count).Error)
	require.EqualValues(t, 1, count)
	// Simulate a crash after log commit but before the outbox delivery marker update.
	require.NoError(t, DB.Model(&ImagineBillingEvent{}).Where("1 = 1").Update("delivered", false).Error)
	require.NoError(t, DeliverImagineBillingEvents())
	require.NoError(t, LOG_DB.Model(&Log{}).Where("request_id = ?", batch.RequestID).Count(&count).Error)
	require.EqualValues(t, 1, count)
	// A late failure cannot refund a completed task.
	require.NoError(t, ApplyImagineResult(tasks[0].ID, ImagineTask{Status: "failed"}))
	require.NoError(t, DB.First(&user, batch.UserID).Error)
	require.Equal(t, 1000000-22520, user.Quota)
}

func TestImagineRepeatActualTasksAndDuplicateRefund(t *testing.T) {
	for _, repeat := range []int{2, 4} {
		t.Run(fmt.Sprint(repeat), func(t *testing.T) {
			ids := []string{}
			for i := 0; i < repeat; i++ {
				ids = append(ids, fmt.Sprint("upstream-", i))
			}
			batch, tasks := setupImagineTest(t, repeat, ids)
			for i, task := range tasks {
				status := "completed"
				if i == 0 {
					status = "failed"
				}
				for n := 0; n < 3; n++ {
					require.NoError(t, ApplyImagineResult(task.ID, ImagineTask{Status: status, Images: `["https://a.test/1"]`}))
				}
			}
			var user User
			require.NoError(t, DB.First(&user, batch.UserID).Error)
			require.Equal(t, 1000000-(repeat-1)*22520, user.Quota)
			require.Equal(t, (repeat-1)*22520, user.UsedQuota)
			fresh, err := GetImagineBatch(batch.ID)
			require.NoError(t, err)
			require.Equal(t, repeat, fresh.ActualTaskCount)
			require.Equal(t, 22520, fresh.RefundedQuota)
			require.Equal(t, "finished", fresh.Status)
		})
	}
}

func TestImagineFailureTransactionRollsBackOnJournalError(t *testing.T) {
	batch, tasks := setupImagineTest(t, 1, []string{"upstream-one"})
	require.NoError(t, DB.Create(&ImagineBillingEvent{ID: tasks[0].ID + ":failed"}).Error)
	require.Error(t, ApplyImagineResult(tasks[0].ID, ImagineTask{Status: "failed"}))
	var user User
	require.NoError(t, DB.First(&user, batch.UserID).Error)
	require.Equal(t, 1000000-22520, user.Quota)
	var task ImagineTask
	require.NoError(t, DB.First(&task, "id = ?", tasks[0].ID).Error)
	require.Equal(t, "queued", task.Status)
}

func TestImagineSubscriptionRefundIsAtomicAndDoesNotCreditWallet(t *testing.T) {
	batch, tasks := setupImagineTest(t, 1, []string{"upstream-subscription"})
	sub := UserSubscription{UserId: batch.UserID, AmountTotal: 1000000, AmountUsed: 22520}
	require.NoError(t, DB.Create(&sub).Error)
	record := SubscriptionPreConsumeRecord{RequestId: batch.RequestID, UserId: batch.UserID, UserSubscriptionId: sub.Id, PreConsumed: 22520, Status: "pre_consumed"}
	require.NoError(t, DB.Create(&record).Error)
	t.Cleanup(func() { DB.Where("id = ?", record.Id).Delete(&SubscriptionPreConsumeRecord{}) })
	require.NoError(t, DB.Model(batch).Updates(map[string]any{"billing_source": "subscription", "subscription_id": sub.Id}).Error)
	for i := 0; i < 4; i++ {
		require.NoError(t, ApplyImagineResult(tasks[0].ID, ImagineTask{Status: "failed"}))
	}
	require.NoError(t, DB.First(&sub, sub.Id).Error)
	require.Zero(t, sub.AmountUsed)
	require.NoError(t, DB.First(&record, record.Id).Error)
	require.Zero(t, record.PreConsumed)
	var user User
	require.NoError(t, DB.First(&user, batch.UserID).Error)
	require.Equal(t, 1000000-22520, user.Quota)
}
