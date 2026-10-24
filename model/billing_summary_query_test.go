package model

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Frozen pre-optimization SQL is the reference for amounts, overlapping LIKE
// matches, NULLs, and user deduplication. Keep it independent of the new source.
func legacyBillingHourlyQuery(db *gorm.DB, since int64) *gorm.DB {
	hourExpr := "(created_at / 3600) * 3600"
	if db.Dialector.Name() == "mysql" {
		hourExpr = "(created_at DIV 3600) * 3600"
	}
	return db.Table("logs").
		Select(hourExpr+` as hour_bucket,
		         model_name,
		         channel_id,
		         SUM(CASE WHEN accounting_status = 'ok' THEN accounting_channel_cost_amount_usd ELSE 0 END) as cost_usd,
		         SUM(CASE WHEN accounting_status = 'ok' THEN CASE WHEN other LIKE '%"billing_source":"subscription"%' THEN quota * 1.0 / `+fmt.Sprintf("%v", common.QuotaPerUnit)+` ELSE accounting_user_final_amount_usd END ELSE 0 END) as revenue_usd,
		         SUM(CASE WHEN accounting_status = 'ok' AND other LIKE '%"billing_source":"subscription"%' THEN accounting_channel_cost_amount_usd * (quota - COALESCE(wallet_supplement_quota, 0)) * 1.0 / quota ELSE 0 END) as subscription_cost_usd,
		         SUM(CASE WHEN accounting_status = 'ok' AND other LIKE '%"billing_source":"subscription"%' THEN (quota - COALESCE(wallet_supplement_quota, 0)) * 1.0 / `+fmt.Sprintf("%v", common.QuotaPerUnit)+` ELSE 0 END) as subscription_billing_usd,
		         SUM(CASE WHEN accounting_status = 'ok' AND other LIKE '%"subscription_type":"gpt_subscription"%' THEN accounting_channel_cost_amount_usd ELSE 0 END) as paid_subscription_cost_usd,
		         SUM(CASE WHEN accounting_status = 'ok' AND other LIKE '%"subscription_type":"gpt_subscription"%' THEN quota * 1.0 / `+fmt.Sprintf("%v", common.QuotaPerUnit)+` ELSE 0 END) as paid_subscription_revenue_usd,
		         SUM(CASE WHEN accounting_status = 'ok' AND other LIKE '%"subscription_type":"coding_plan"%' THEN accounting_channel_cost_amount_usd ELSE 0 END) as coding_plan_cost_usd,
		         SUM(CASE WHEN accounting_status = 'ok' AND other LIKE '%"subscription_type":"coding_plan"%' THEN quota * 1.0 / `+fmt.Sprintf("%v", common.QuotaPerUnit)+` ELSE 0 END) as coding_plan_revenue_usd,
		         SUM(CASE WHEN accounting_status = 'ok' THEN 1 ELSE 0 END) as request_count,
		         COUNT(*) as accounting_target_request_count`).
		Where("type = ? AND quota > 0 AND accounting_status <> '' AND created_at >= ?", LogTypeConsume, since).
		Group(hourExpr + ", model_name, channel_id")
}

func legacyBillingActivityQuery(db *gorm.DB, day int64) *gorm.DB {
	dayExpr := billingDayExpr("created_at")
	return db.Table("logs").Select(dayExpr+` AS day, model_name, channel_id, user_id,
        MAX(CASE WHEN (COALESCE(wallet_supplement_quota, 0) > 0 OR COALESCE(other, '') LIKE '%"billing_source":"wallet"%' OR (COALESCE(other, '') NOT LIKE '%"billing_source":"subscription"%' AND NOT (COALESCE(other, '') LIKE '%"subscription_type":"gpt_trial"%' OR COALESCE(other, '') LIKE '%"subscription_type":"gpt_referral_reward"%') AND NOT (COALESCE(other, '') LIKE '%"subscription_type":"gpt_subscription"%' OR COALESCE(other, '') LIKE '%"subscription_type":"coding_plan"%'))) THEN 1 ELSE 0 END) AS wallet,
        MAX(CASE WHEN (COALESCE(other, '') LIKE '%"subscription_type":"gpt_trial"%' OR COALESCE(other, '') LIKE '%"subscription_type":"gpt_referral_reward"%') THEN 1 ELSE 0 END) AS experience,
        MAX(CASE WHEN COALESCE(other, '') LIKE '%"subscription_type":"gpt_subscription"%' THEN 1 ELSE 0 END) AS paid_subscription,
        MAX(CASE WHEN COALESCE(other, '') LIKE '%"subscription_type":"coding_plan"%' THEN 1 ELSE 0 END) AS coding_plan`).
		Where("type = ? AND quota > 0 AND accounting_status = 'ok' AND created_at >= ? AND created_at < ?", LogTypeConsume, day, day+86400).
		Group(dayExpr + ", model_name, channel_id, user_id")
}

// Use only isolated development databases for the optional server dialects.
// Temporary tables shadow logs on the transaction's dedicated connection;
// cleanup never drops or migrates a persistent logs table.
func TestBillingRollupQueriesPreserveLegacyResults(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres", "mysql"} {
		t.Run(dialect, func(t *testing.T) {
			var dialector gorm.Dialector
			switch dialect {
			case "sqlite":
				dialector = sqlite.Open(":memory:")
			case "postgres":
				dsn := os.Getenv("APIMASTER_TEST_POSTGRES_DSN")
				if dsn == "" {
					t.Skip("APIMASTER_TEST_POSTGRES_DSN not set")
				}
				dialector = postgres.Open(dsn)
			case "mysql":
				dsn := os.Getenv("APIMASTER_TEST_MYSQL_DSN")
				if dsn == "" {
					t.Skip("APIMASTER_TEST_MYSQL_DSN not set")
				}
				dialector = mysql.Open(dsn)
			}
			db, err := gorm.Open(dialector, &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
			require.NoError(t, err)
			pool, err := db.DB()
			require.NoError(t, err)
			defer pool.Close()
			pool.SetMaxOpenConns(1)
			tx := db.Begin()
			require.NoError(t, tx.Error)
			defer tx.Rollback()
			oldMySQL := common.UsingMySQL
			common.UsingMySQL = dialect == "mysql"
			defer func() { common.UsingMySQL = oldMySQL }()
			require.NoError(t, tx.Exec(`CREATE TEMPORARY TABLE logs (
                id BIGINT PRIMARY KEY, created_at BIGINT, type INTEGER,
                model_name VARCHAR(256), channel_id INTEGER, user_id INTEGER,
                quota BIGINT, wallet_supplement_quota BIGINT,
                accounting_status VARCHAR(32),
                accounting_channel_cost_amount_usd DECIMAL(20,10),
                accounting_user_final_amount_usd DECIMAL(20,10), other TEXT
            )`).Error)
			const day int64 = 1792771200 // 2026-10-24 00:00 Beijing
			fixture := func(id int, timestamp int64, text interface{}, status interface{}, quota int, supplement interface{}) {
				t.Helper()
				require.NoError(t, tx.Exec(`INSERT INTO logs
                    (id,created_at,type,model_name,channel_id,user_id,quota,wallet_supplement_quota,accounting_status,accounting_channel_cost_amount_usd,accounting_user_final_amount_usd,other)
                    VALUES (?,?,2,?,?,?,?,?,?,'0.1234567891','0.9876543219',?)`,
					id, timestamp, fmt.Sprintf("model-%d", id%2), id%3, id%4, quota, supplement, status, text).Error)
			}
			texts := []interface{}{
				nil, "", `{}`, `not valid JSON`,
				`{"billing_source":"wallet"}`,
				`{"billing_source":"subscription","subscription_type":"gpt_trial"}`,
				`{"billing_source":"subscription","subscription_type":"gpt_referral_reward"}`,
				`{"billing_source":"subscription","subscription_type":"gpt_subscription"}`,
				`{"billing_source":"subscription","subscription_type":"coding_plan"}`,
				`{"billing_source":"subscription"}`,
				// Preserve legacy spacing, LIKE wildcard, and overlapping matches.
				`{"billing_source": "subscription", "subscription_type": "gpt_trial"}`,
				`{"billingXsource":"subscription","subscriptionXtype":"gptXsubscription"}`,
				`{"a":{"subscription_type":"gpt_trial"},"b":{"subscription_type":"coding_plan"},"billing_source":"wallet"}`,
				`{"padding":"` + strings.Repeat("large-log-text-", 2048) + `","billing_source":"subscription","subscription_type":"coding_plan"}`,
			}
			for i, text := range texts {
				for j, timestamp := range []int64{day - 86401, day - 86400, day - 1, day, day + 3660, day + 86399, day + 86400} {
					fixture(1+i*10+j, timestamp, text, "ok", 500000, nil)
				}
			}
			fixture(200, day+60, `{"billing_source":"subscription","subscription_type":"gpt_trial"}`, "ok", 500000, 125000)
			fixture(201, day+61, nil, "refunded", 500000, nil)
			fixture(202, day+62, nil, "partial", 500000, nil)
			fixture(203, day+63, nil, "", 500000, nil)
			fixture(204, day+64, nil, nil, 500000, nil)
			fixture(205, day+65, nil, "ok", 0, nil)
			fixture(206, day+66, nil, "ok", -1, nil)
			fixture(207, day+67, nil, "ok", 500000, nil)
			require.NoError(t, tx.Exec("UPDATE logs SET type = 6 WHERE id = 207").Error)
			compare := func() {
				t.Helper()
				var wantHourly, gotHourly []BillingHourlySummary
				require.NoError(t, legacyBillingHourlyQuery(tx, day-26*3600).Scan(&wantHourly).Error)
				require.NoError(t, billingHourlySummaryQuery(tx, day-26*3600).Scan(&gotHourly).Error)
				require.NotEmpty(t, wantHourly)
				require.ElementsMatch(t, wantHourly, gotHourly)
				for _, date := range []int64{day - 86400, day, day + 86400} {
					var wantDaily, gotDaily []BillingDailyUserActivity
					require.NoError(t, legacyBillingActivityQuery(tx, date).Scan(&wantDaily).Error)
					require.NoError(t, billingDailyUserActivityQuery(tx, date).Scan(&gotDaily).Error)
					require.NotEmpty(t, wantDaily)
					require.ElementsMatch(t, wantDaily, gotDaily)
				}
			}
			compare()
			// A delayed settlement and a refund must both be picked up on rerun.
			fixture(208, day-3600, `{"billing_source":"wallet"}`, "ok", 500000, nil)
			require.NoError(t, tx.Exec("UPDATE logs SET accounting_status = 'refunded' WHERE id = 200").Error)
			compare()
		})
	}
}
