package model

import (
	"fmt"
	"gorm.io/driver/postgres"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func imageRefundFixture(t *testing.T) (*Task, *Log, *Log) {
	t.Helper()
	if dsn := os.Getenv("IMAGE_REFUND_TEST_DSN"); dsn != "" {
		oldDB := DB
		admin, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
		require.NoError(t, err)
		schema := "image_refund_" + strings.ToLower(t.Name())
		require.NoError(t, admin.Exec("CREATE SCHEMA "+schema).Error)
		DB, err = gorm.Open(postgres.Open(dsn+" search_path="+schema), &gorm.Config{})
		require.NoError(t, err)
		t.Cleanup(func() {
			DB = oldDB
			require.NoError(t, admin.Exec("DROP SCHEMA "+schema+" CASCADE").Error)
			sqlAdmin, _ := admin.DB()
			_ = sqlAdmin.Close()
		})
		require.NoError(t, DB.AutoMigrate(&User{}, &Token{}))
	} else {
		setupAtomicBillingTestDB(t)
	}
	sqlDB, err := DB.DB()
	require.NoError(t, err)
	if os.Getenv("IMAGE_REFUND_TEST_DSN") == "" {
		sqlDB.SetMaxOpenConns(1)
	}
	oldLog := LOG_DB
	LOG_DB = DB
	t.Cleanup(func() { LOG_DB = oldLog; _ = sqlDB.Close() })
	require.NoError(t, DB.AutoMigrate(&Task{}, &Log{}, &ImageTaskRefund{}, &ImageRefundLogDelivery{}, &UserSubscription{}))
	require.NoError(t, DB.Create(&User{Id: 1, Username: "image-user", Quota: 100, UsedQuota: 80}).Error)
	require.NoError(t, DB.Create(&Token{Id: 2, UserId: 1, Key: "image-token", RemainQuota: 100, UsedQuota: 80}).Error)
	task := &Task{UserId: 1, TaskID: "image-task", Status: TaskStatusFailure}
	require.NoError(t, DB.Create(task).Error)
	consume := &Log{UserId: 1, TokenId: 2, Type: LogTypeConsume, Quota: 40, Other: common.MapToJsonStr(map[string]interface{}{"task_id": task.TaskID})}
	require.NoError(t, LOG_DB.Create(consume).Error)
	refund := &Log{UserId: 1, TokenId: 2, Type: LogTypeRefund, Quota: 40, Other: common.MapToJsonStr(map[string]interface{}{"task_id": task.TaskID, "refund_source": "image_async_task_failure"})}
	return task, consume, refund
}
func assertImageBalances(t *testing.T, wallet, used, tokenRemain, tokenUsed int) {
	t.Helper()
	var user User
	var token Token
	require.NoError(t, DB.First(&user, 1).Error)
	require.NoError(t, DB.Unscoped().First(&token, 2).Error)
	require.Equal(t, wallet, user.Quota)
	require.Equal(t, used, user.UsedQuota)
	require.Equal(t, tokenRemain, token.RemainQuota)
	require.Equal(t, tokenUsed, token.UsedQuota)
}
func TestImageRefundConcurrentFreezeAndApplyOnce(t *testing.T) {
	task, consume, refund := imageRefundFixture(t)
	var wg sync.WaitGroup
	failures := make(chan error, 32)
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			copyRefund := *refund
			item, err := FreezeImageTaskRefund(task, consume, &copyRefund, "wallet", 0)
			if err == nil {
				_, err = ApplyImageTaskRefund(item.ID)
			}
			failures <- err
		}()
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		require.NoError(t, err)
	}
	assertImageBalances(t, 140, 40, 140, 40)
	var count int64
	require.NoError(t, DB.Model(&Log{}).Where("type = ?", LogTypeRefund).Count(&count).Error)
	require.EqualValues(t, 1, count)
	require.NoError(t, RetryImageTaskRefunds())
	assertImageBalances(t, 140, 40, 140, 40)
	var row Log
	require.NoError(t, LOG_DB.First(&row, consume.Id).Error)
	require.Contains(t, row.Other, `"used_quota_reversed":true`)
}
func TestImageRefundTokenFailureRollsBackAndWorkerRecovers(t *testing.T) {
	task, consume, refund := imageRefundFixture(t)
	item, err := FreezeImageTaskRefund(task, consume, refund, "wallet", 0)
	require.NoError(t, err)
	require.NoError(t, DB.Model(&Token{}).Where("id = ?", 2).Update("user_id", 99).Error)
	_, err = ApplyImageTaskRefund(item.ID)
	require.Error(t, err)
	assertImageBalances(t, 100, 80, 100, 80)
	saved, err := GetImageTaskRefund(1, task.TaskID)
	require.NoError(t, err)
	require.Equal(t, "pending", saved.Status)
	require.NoError(t, DB.Model(&Token{}).Where("id = ?", 2).Update("user_id", 1).Error)
	require.NoError(t, RetryImageTaskRefunds())
	assertImageBalances(t, 140, 40, 140, 40)
}
func TestImageRefundLogFailureRollsBackMoney(t *testing.T) {
	task, consume, refund := imageRefundFixture(t)
	item, err := FreezeImageTaskRefund(task, consume, refund, "wallet", 0)
	require.NoError(t, err)
	require.NoError(t, DB.Migrator().DropTable(&ImageRefundLogDelivery{}))
	_, err = ApplyImageTaskRefund(item.ID)
	require.Error(t, err)
	assertImageBalances(t, 100, 80, 100, 80)
	require.NoError(t, DB.AutoMigrate(&ImageRefundLogDelivery{}))
	require.NoError(t, RetryImageTaskRefunds())
	assertImageBalances(t, 140, 40, 140, 40)
}
func TestImageRefundSubscriptionSupplementAndDeletedToken(t *testing.T) {
	task, consume, refund := imageRefundFixture(t)
	consume.WalletSupplementQuota = 10
	require.NoError(t, LOG_DB.Save(consume).Error)
	require.NoError(t, DB.Create(&UserSubscription{Id: 7, UserId: 1, AmountUsed: 80}).Error)
	require.NoError(t, DB.Delete(&Token{}, 2).Error)
	item, err := FreezeImageTaskRefund(task, consume, refund, "subscription", 7)
	require.NoError(t, err)
	_, err = ApplyImageTaskRefund(item.ID)
	require.NoError(t, err)
	assertImageBalances(t, 110, 40, 140, 40)
	var sub UserSubscription
	require.NoError(t, DB.First(&sub, 7).Error)
	require.EqualValues(t, 50, sub.AmountUsed)
}
func TestImageRefundSplitLogDatabaseDeliveryRecovery(t *testing.T) {
	task, consume, refund := imageRefundFixture(t)
	separate, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s-log?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := separate.DB()
	require.NoError(t, err)
	if os.Getenv("IMAGE_REFUND_TEST_DSN") == "" {
		sqlDB.SetMaxOpenConns(1)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	LOG_DB = separate
	require.NoError(t, LOG_DB.AutoMigrate(&Log{}))
	require.NoError(t, LOG_DB.Create(consume).Error)
	item, err := FreezeImageTaskRefund(task, consume, refund, "wallet", 0)
	require.NoError(t, err)
	_, err = ApplyImageTaskRefund(item.ID)
	require.Error(t, err)
	assertImageBalances(t, 140, 40, 140, 40)
	require.NoError(t, LOG_DB.AutoMigrate(&ImageRefundLogDelivery{}))
	require.NoError(t, RetryImageTaskRefunds())
	// Simulate crash after LOG_DB commit but before main DB delivery acknowledgement.
	require.NoError(t, DB.Model(item).Update("log_published", false).Error)
	require.NoError(t, RetryImageTaskRefunds())
	assertImageBalances(t, 140, 40, 140, 40)
	var count int64
	require.NoError(t, LOG_DB.Model(&Log{}).Where("type = ?", LogTypeRefund).Count(&count).Error)
	require.EqualValues(t, 1, count)
}
