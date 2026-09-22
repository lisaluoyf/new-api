package model

import (
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestBillingHoldRefundRejectsWrongTokenOwner(t *testing.T) {
	setupBillingHoldSubscriptionTestDB(t)
	require.NoError(t, DB.AutoMigrate(&Token{}))
	require.NoError(t, DB.Create(&User{Id: 1, Username: "refund-owner", Quota: 1000}).Error)
	require.NoError(t, DB.Create(&Token{Id: 1, UserId: 2, Key: "different-owner", RemainQuota: 500}).Error)
	hold := BillingHold{RequestId: "wrong-token-owner", UserId: 1, TokenId: 1, PreConsumedQuota: 100, Status: "processing"}
	require.NoError(t, DB.Create(&hold).Error)
	require.ErrorContains(t, ResolveBillingHoldRefund(&hold, false, "not charged", ""), "owner mismatch")
	var user User
	require.NoError(t, DB.First(&user, 1).Error)
	require.Equal(t, 1000, user.Quota, "wallet credit must roll back when ownership is inconsistent")
	current, err := GetBillingHoldById(hold.Id)
	require.NoError(t, err)
	require.Equal(t, "processing", current.Status)
}

func TestBillingHoldClaimAndStaleLease(t *testing.T) {
	setupBillingHoldSubscriptionTestDB(t)
	now := common.GetTimestamp()
	hold := BillingHold{RequestId: "legacy-processing", UserId: 1, PreConsumedQuota: 100,
		Status: "processing", CreatedAt: now - 600, ResolvedAt: 0}
	require.NoError(t, DB.Create(&hold).Error)
	due, err := ListDueBillingHolds(now, 200)
	require.NoError(t, err)
	require.Len(t, due, 1)
	claimed, err := ClaimBillingHold(hold.Id)
	require.NoError(t, err)
	require.True(t, claimed, "legacy processing rows without a timestamp must be reclaimable")
	require.Error(t, ResolveBillingHoldRefund(&hold, false, "stale attempt", ""))
	require.Error(t, RescheduleBillingHold(hold.Id, now+300, "stale retry", hold.ResolvedAt))
	current, err := GetBillingHoldById(hold.Id)
	require.NoError(t, err)
	require.Equal(t, "processing", current.Status)
	require.GreaterOrEqual(t, current.ResolvedAt, now)
}

func TestBillingHoldRefundConcurrentPostgres(t *testing.T) {
	dsn := os.Getenv("APIMASTER_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("APIMASTER_TEST_POSTGRES_DSN not set")
	}
	admin, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	adminPool, err := admin.DB()
	require.NoError(t, err)
	defer adminPool.Close()
	schema := fmt.Sprintf("hold_refund_test_%d", time.Now().UnixNano())
	require.NoError(t, admin.Exec("CREATE SCHEMA "+schema).Error)
	defer admin.Exec("DROP SCHEMA " + schema + " CASCADE")
	config, err := pgx.ParseConfig(dsn)
	require.NoError(t, err)
	config.RuntimeParams["search_path"] = schema
	pool := stdlib.OpenDB(*config)
	defer pool.Close()
	db, err := gorm.Open(postgres.New(postgres.Config{Conn: pool}), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&User{}, &Token{}, &UserSubscription{}, &SubscriptionPreConsumeRecord{}, &BillingHold{}))
	oldDB, oldRedis := DB, common.RedisEnabled
	DB, common.RedisEnabled = db, false
	defer func() { DB, common.RedisEnabled = oldDB, oldRedis }()
	require.NoError(t, DB.Create(&User{Id: 1, Username: "concurrent-refund", Quota: 1000}).Error)
	token := Token{Id: 1, UserId: 1, Key: "deleted-concurrent-token", RemainQuota: 500, UsedQuota: 100}
	require.NoError(t, DB.Create(&token).Error)
	require.NoError(t, DB.Delete(&token).Error)
	hold := BillingHold{RequestId: "concurrent-refund", UserId: 1, TokenId: 1, PreConsumedQuota: 100,
		Status: "processing", ResolvedAt: common.GetTimestamp()}
	require.NoError(t, DB.Create(&hold).Error)
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); <-start; results <- ResolveBillingHoldRefund(&hold, false, "not charged", "") }()
	}
	close(start)
	wg.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		}
	}
	require.Equal(t, 1, successes, "only one financial transition may commit")
	var user User
	require.NoError(t, DB.First(&user, 1).Error)
	require.Equal(t, 1100, user.Quota)
	require.NoError(t, DB.Unscoped().First(&token, 1).Error)
	require.Equal(t, 600, token.RemainQuota)
	require.Zero(t, token.UsedQuota)
	require.True(t, token.DeletedAt.Valid)
	current, err := GetBillingHoldById(hold.Id)
	require.NoError(t, err)
	require.Equal(t, BillingHoldStatusRefunded, current.Status)
}
