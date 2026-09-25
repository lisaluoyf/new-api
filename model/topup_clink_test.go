package model

import (
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func setupClinkSettlementDB(t *testing.T, db *gorm.DB) {
	t.Helper()
	oldDB, oldLogDB, oldRedis := DB, LOG_DB, common.RedisEnabled
	DB, LOG_DB, common.RedisEnabled = db, db, false
	t.Cleanup(func() { DB, LOG_DB, common.RedisEnabled = oldDB, oldLogDB, oldRedis })
	require.NoError(t, db.AutoMigrate(&User{}, &TopUp{}, &Log{}))
	require.NoError(t, db.Create(&User{Id: 12833, Username: "clink-test", Quota: 50}).Error)
}

func TestClinkSettlementRecoveryAndSafety(t *testing.T) {
	for _, tt := range []struct {
		name, status string
		verified     bool
		provider     string
		amount       float64
		refund       float64
		completed    int64
		missingUser  bool
		ok           bool
	}{
		{name: "pending callback", status: "pending", provider: "clink", amount: 1, ok: true},
		{name: "verified failed recovery", status: "failed", verified: true, provider: "clink", amount: 1, ok: true},
		{name: "failed needs query", status: "failed", provider: "clink", amount: 1},
		{name: "wrong amount", status: "failed", verified: true, provider: "clink", amount: 2},
		{name: "wrong provider", status: "pending", verified: true, provider: "paypal", amount: 1},
		{name: "refunded", status: "refunded", verified: true, provider: "clink", amount: 1},
		{name: "refund evidence", status: "failed", verified: true, provider: "clink", amount: 1, refund: 1},
		{name: "previous completion", status: "failed", verified: true, provider: "clink", amount: 1, completed: 1},
		{name: "missing user rollback", status: "pending", verified: true, provider: "clink", amount: 1, missingUser: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
			require.NoError(t, err)
			pool, err := db.DB()
			require.NoError(t, err)
			t.Cleanup(func() { pool.Close() })
			setupClinkSettlementDB(t, db)
			userID := 12833
			if tt.missingUser {
				userID = 99999
			}
			order := TopUp{UserId: userID, TradeNo: "CLINK-test", Status: tt.status, PaymentProvider: tt.provider, PaymentMethod: "clink", Amount: 1, Money: 1, RefundedAmount: tt.refund, CompleteTime: tt.completed}
			require.NoError(t, db.Create(&order).Error)
			settle := func() error {
				if tt.verified {
					return RechargeClinkVerified(order.TradeNo, "", tt.amount)
				}
				return RechargeClink(order.TradeNo, "")
			}
			err = settle()
			if tt.ok {
				require.NoError(t, err)
				require.NoError(t, settle())
			} else {
				require.Error(t, err)
			}
			var user User
			require.NoError(t, db.First(&user, 12833).Error)
			expected := 50
			if tt.ok {
				expected += int(common.QuotaPerUnit)
			}
			require.Equal(t, expected, user.Quota)
			var current TopUp
			require.NoError(t, db.First(&current, order.Id).Error)
			if tt.ok {
				require.Equal(t, "success", current.Status)
				require.Equal(t, float64(1), current.CreditedAmount)
				require.Positive(t, current.CompleteTime)
			} else {
				require.Equal(t, tt.status, current.Status)
			}
			var logs int64
			require.NoError(t, db.Model(&Log{}).Where("type = ?", LogTypeTopup).Count(&logs).Error)
			if tt.ok {
				require.EqualValues(t, 1, logs)
			} else {
				require.Zero(t, logs)
			}
		})
	}
}

func TestClinkConcurrentSettlementPostgres(t *testing.T) {
	dsn := os.Getenv("APIMASTER_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("APIMASTER_TEST_POSTGRES_DSN not set")
	}
	admin, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	ap, err := admin.DB()
	require.NoError(t, err)
	defer ap.Close()
	schema := fmt.Sprintf("clink_test_%d", time.Now().UnixNano())
	require.NoError(t, admin.Exec("CREATE SCHEMA "+schema).Error)
	defer admin.Exec("DROP SCHEMA " + schema + " CASCADE")
	config, err := pgx.ParseConfig(dsn)
	require.NoError(t, err)
	config.RuntimeParams["search_path"] = schema
	pool := stdlib.OpenDB(*config)
	defer pool.Close()
	db, err := gorm.Open(postgres.New(postgres.Config{Conn: pool}), &gorm.Config{})
	require.NoError(t, err)
	setupClinkSettlementDB(t, db)
	order := TopUp{UserId: 12833, TradeNo: "CLINK-concurrent", Status: "failed", PaymentProvider: "clink", PaymentMethod: "clink", Amount: 1, Money: 1}
	require.NoError(t, db.Create(&order).Error)
	start := make(chan struct{})
	results := make(chan error, 8)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); <-start; results <- RechargeClinkVerified(order.TradeNo, "", 1) }()
	}
	close(start)
	wg.Wait()
	close(results)
	for err := range results {
		require.NoError(t, err)
	}
	var user User
	require.NoError(t, db.First(&user, 12833).Error)
	require.Equal(t, 50+int(common.QuotaPerUnit), user.Quota)
	var logs int64
	require.NoError(t, db.Model(&Log{}).Where("type = ?", LogTypeTopup).Count(&logs).Error)
	require.EqualValues(t, 1, logs)
	require.Error(t, UpdatePendingTopUpStatus(order.TradeNo, "clink", "failed"))
	var current TopUp
	require.NoError(t, db.First(&current, order.Id).Error)
	require.Equal(t, "success", current.Status)
}
