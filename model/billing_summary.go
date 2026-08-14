package model

import (
	"fmt"
	"sort"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// BillingHourlySummary is a pre-aggregated rollup of Log accounting fields,
// grain = (hour_bucket, model_name, channel_id). Refreshed hourly by
// service.StartBillingSummaryTask(). Backs the 平台账单 dashboard's default
// view (no token/username/email filter). Lives in LOG_DB since it's built
// from the logs table, which itself may live in a separate LOG_SQL_DSN db.
type BillingHourlySummary struct {
	Id                         int64   `json:"id" gorm:"primaryKey;autoIncrement"`
	HourBucket                 int64   `json:"hour_bucket" gorm:"uniqueIndex:idx_bill_hour_model_ch;index;not null"` // unix seconds, floored to the hour
	ModelName                  string  `json:"model_name" gorm:"size:256;uniqueIndex:idx_bill_hour_model_ch;default:''"`
	ChannelId                  int     `json:"channel_id" gorm:"uniqueIndex:idx_bill_hour_model_ch;default:0"`
	CostUSD                    float64 `json:"cost_usd" gorm:"type:decimal(20,10);default:0"`                 // SUM(accounting_channel_cost_amount_usd)
	RevenueUSD                 float64 `json:"revenue_usd" gorm:"type:decimal(20,10);default:0"`              // SUM(accounting_user_final_amount_usd) + subscription official billing
	SubscriptionCostUSD        float64 `json:"subscription_cost_usd" gorm:"type:decimal(20,10);default:0"`    // SUM(accounting_channel_cost_amount_usd) where billing_source=subscription
	SubscriptionBillingUSD     float64 `json:"subscription_billing_usd" gorm:"type:decimal(20,10);default:0"` // SUM(subscription official price, quota / QuotaPerUnit) where billing_source=subscription
	PaidSubscriptionCostUSD    float64 `json:"paid_subscription_cost_usd" gorm:"type:decimal(20,10);default:0"`
	PaidSubscriptionRevenueUSD float64 `json:"paid_subscription_revenue_usd" gorm:"type:decimal(20,10);default:0"`
	RequestCount               int64   `json:"request_count" gorm:"default:0"`
	UpdatedAt                  int64   `json:"updated_at"`
}

// BillingWalletDailySnapshot stores the latest non-admin wallet balance seen
// for each Beijing calendar day. Hourly refreshes overwrite the same day, so
// the retained value is the day's last available snapshot.
type BillingWalletDailySnapshot struct {
	Day              int64   `json:"day" gorm:"primaryKey;not null"`
	WalletBalanceUSD float64 `json:"wallet_balance_usd" gorm:"type:decimal(20,6);default:0"`
	SnapshotAt       int64   `json:"snapshot_at" gorm:"not null"`
}

// BillingSubscriptionDailySnapshot stores the latest non-admin subscription
// balance seen for each Beijing calendar day. Like wallet snapshots, hourly
// refreshes overwrite the same day so the retained value is the day's latest
// available snapshot.
type BillingSubscriptionDailySnapshot struct {
	Day                    int64   `json:"day" gorm:"primaryKey;not null"`
	SubscriptionBalanceUSD float64 `json:"subscription_balance_usd" gorm:"type:decimal(20,6);default:0"`
	SnapshotAt             int64   `json:"snapshot_at" gorm:"not null"`
}

// UpsertBillingHourlySummaries writes/merges rows keyed by (hour_bucket, model_name, channel_id).
func UpsertBillingHourlySummaries(rows []BillingHourlySummary) error {
	if len(rows) == 0 {
		return nil
	}
	return LOG_DB.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "hour_bucket"}, {Name: "model_name"}, {Name: "channel_id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"cost_usd", "revenue_usd", "subscription_cost_usd", "subscription_billing_usd", "paid_subscription_cost_usd", "paid_subscription_revenue_usd", "request_count", "updated_at",
		}),
	}).Create(&rows).Error
}

// BillingDailyRow is one day's aggregated cost/revenue, returned to the
// 平台账单 page. Profit and margin are derived at query time, not stored.
type BillingDailyRow struct {
	Day                        int64    `json:"day" gorm:"column:day"` // unix seconds, floored to Beijing (UTC+8) midnight
	CostUSD                    float64  `json:"cost_usd" gorm:"column:cost_usd"`
	RevenueUSD                 float64  `json:"revenue_usd" gorm:"column:revenue_usd"`
	SubscriptionCostUSD        float64  `json:"subscription_cost_usd" gorm:"column:subscription_cost_usd"`
	SubscriptionBillingUSD     float64  `json:"subscription_billing_usd" gorm:"column:subscription_billing_usd"`
	PaidSubscriptionCostUSD    float64  `json:"paid_subscription_cost_usd" gorm:"column:paid_subscription_cost_usd"`
	PaidSubscriptionRevenueUSD float64  `json:"paid_subscription_revenue_usd" gorm:"column:paid_subscription_revenue_usd"`
	AccountingOKRequestCount   int64    `json:"accounting_ok_request_count" gorm:"column:accounting_ok_request_count"`
	AccountingTargetReqCount   int64    `json:"accounting_target_request_count" gorm:"column:accounting_target_request_count"`
	NonSubscriptionUserCount   int64    `json:"non_subscription_user_count" gorm:"-"`
	SubscriptionUserCount      int64    `json:"subscription_user_count" gorm:"-"`
	WalletBalanceUSD           *float64 `json:"wallet_balance_usd,omitempty" gorm:"-"`
	SubscriptionBalanceUSD     *float64 `json:"subscription_balance_usd,omitempty" gorm:"-"`
}

type billingDailyCountRow struct {
	Day                      int64 `gorm:"column:day"`
	AccountingTargetReqCount int64 `gorm:"column:accounting_target_request_count"`
}

type billingDailyUserCountRow struct {
	Day                      int64 `gorm:"column:day"`
	NonSubscriptionUserCount int64 `gorm:"column:non_subscription_user_count"`
	SubscriptionUserCount    int64 `gorm:"column:subscription_user_count"`
}

type billingUserCountTotals struct {
	NonSubscriptionUserCount int64 `json:"non_subscription_user_count"`
	SubscriptionUserCount    int64 `json:"subscription_user_count"`
}

// 日分桶按北京时间（UTC+8，无夏令时）切天，使账单页的"每天"与使用日志页
// （浏览器本地时间筛选，团队在北京）看到的同一天严格对齐。
const billingDayTZOffsetSeconds = 8 * 3600

// billingDayExpr returns a cross-DB SQL expression flooring the given unix-seconds
// column to Beijing midnight. MySQL's `/` is float division, so it needs DIV;
// PostgreSQL and SQLite floor with plain integer `/`.
func billingDayExpr(col string) string {
	if common.UsingMySQL {
		return fmt.Sprintf("((%s + %d) DIV 86400) * 86400 - %d", col, billingDayTZOffsetSeconds, billingDayTZOffsetSeconds)
	}
	return fmt.Sprintf("((%s + %d) / 86400) * 86400 - %d", col, billingDayTZOffsetSeconds, billingDayTZOffsetSeconds)
}

func billingTargetRequestCountExpr() string {
	return "CASE WHEN quota > 0 AND accounting_status <> '' THEN 1 ELSE 0 END"
}

// GetBillingDailyFromSummary aggregates the small pre-computed
// billing_hourly_summaries table down to daily rows. Fast regardless of how
// large the raw logs table has grown, since this table's size only depends
// on (hours × distinct models × distinct channels).
func GetBillingDailyFromSummary(startTimestamp, endTimestamp int64, modelName string, channel int) ([]BillingDailyRow, error) {
	dayExpr := billingDayExpr("hour_bucket")
	tx := LOG_DB.Table("billing_hourly_summaries").
		Select(dayExpr + ` as day,
			SUM(cost_usd) as cost_usd,
			SUM(revenue_usd) as revenue_usd,
			COALESCE(SUM(subscription_cost_usd), 0) as subscription_cost_usd,
			COALESCE(SUM(subscription_billing_usd), 0) as subscription_billing_usd,
			COALESCE(SUM(paid_subscription_cost_usd), 0) as paid_subscription_cost_usd,
			COALESCE(SUM(paid_subscription_revenue_usd), 0) as paid_subscription_revenue_usd,
			SUM(request_count) as accounting_ok_request_count`)
	if startTimestamp != 0 {
		tx = tx.Where("hour_bucket >= ?", startTimestamp)
	}
	if endTimestamp != 0 {
		tx = tx.Where("hour_bucket <= ?", endTimestamp)
	}
	if modelName != "" {
		tx = tx.Where("model_name = ?", modelName)
	}
	if channel != 0 {
		tx = tx.Where("channel_id = ?", channel)
	}
	var rows []BillingDailyRow
	err := tx.Group(dayExpr).Order("day desc").Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	counts, err := getBillingDailyTargetRequestCounts(startTimestamp, endTimestamp, modelName, channel, "", "", "")
	if err != nil {
		return nil, err
	}
	mergeBillingDailyTargetRequestCounts(&rows, counts)
	userCounts, err := getBillingDailyUserCounts(startTimestamp, endTimestamp, modelName, channel, "", "", "")
	if err != nil {
		return nil, err
	}
	mergeBillingDailyUserCounts(&rows, userCounts)
	return rows, nil
}

// GetBillingDailyFromRawLogs aggregates directly from the logs table, for
// filter combinations (token name / username / email) not covered by the
// summary table's grain. Filters directly on logs' own denormalized
// username/token_name columns (both indexed) — same idiom as GetAllLogs,
// no need to resolve names to ids first. Email is resolved to username via
// model.DB (not LOG_DB) first, so this still works when LOG_SQL_DSN points
// LOG_DB at a separate database from the one holding the users table.
func GetBillingDailyFromRawLogs(startTimestamp, endTimestamp int64, modelName string, channel int, tokenName, username, email string) ([]BillingDailyRow, error) {
	dayExpr := billingDayExpr("created_at")
	tx := LOG_DB.Table("logs").
		Select(dayExpr+` as day,
			SUM(CASE WHEN quota > 0 AND accounting_status = 'ok' THEN accounting_channel_cost_amount_usd ELSE 0 END) as cost_usd,
			SUM(CASE WHEN quota > 0 AND accounting_status = 'ok' THEN CASE WHEN other LIKE '%"billing_source":"subscription"%' THEN quota * 1.0 / `+fmt.Sprintf("%v", common.QuotaPerUnit)+` ELSE accounting_user_final_amount_usd END ELSE 0 END) as revenue_usd,
			SUM(CASE WHEN quota > 0 AND accounting_status = 'ok' AND other LIKE '%"billing_source":"subscription"%' THEN accounting_channel_cost_amount_usd ELSE 0 END) as subscription_cost_usd,
			SUM(CASE WHEN quota > 0 AND accounting_status = 'ok' AND other LIKE '%"billing_source":"subscription"%' THEN quota * 1.0 / `+fmt.Sprintf("%v", common.QuotaPerUnit)+` ELSE 0 END) as subscription_billing_usd,
			SUM(CASE WHEN quota > 0 AND accounting_status = 'ok' AND other LIKE '%"subscription_type":"gpt_subscription"%' THEN accounting_channel_cost_amount_usd ELSE 0 END) as paid_subscription_cost_usd,
			SUM(CASE WHEN quota > 0 AND accounting_status = 'ok' AND other LIKE '%"subscription_type":"gpt_subscription"%' THEN quota * 1.0 / `+fmt.Sprintf("%v", common.QuotaPerUnit)+` ELSE 0 END) as paid_subscription_revenue_usd,
			SUM(CASE WHEN quota > 0 AND accounting_status = 'ok' THEN 1 ELSE 0 END) as accounting_ok_request_count,
			SUM(`+billingTargetRequestCountExpr()+`) as accounting_target_request_count`).
		Where("type = ?", LogTypeConsume)

	if startTimestamp != 0 {
		tx = tx.Where("created_at >= ?", startTimestamp)
	}
	if endTimestamp != 0 {
		tx = tx.Where("created_at <= ?", endTimestamp)
	}
	if modelName != "" {
		tx = tx.Where("model_name = ?", modelName)
	}
	if channel != 0 {
		tx = tx.Where("channel_id = ?", channel)
	}
	if tokenName != "" {
		tx = tx.Where("token_name = ?", tokenName)
	}
	if username != "" {
		tx = tx.Where("username = ?", username)
	}
	if email != "" {
		var resolvedUsername string
		err := DB.Table("users").Select("username").Where("email = ?", email).Limit(1).Scan(&resolvedUsername).Error
		if err != nil {
			return nil, err
		}
		if resolvedUsername == "" {
			// No user matches this email — return no rows rather than an
			// unfiltered aggregate.
			return []BillingDailyRow{}, nil
		}
		tx = tx.Where("username = ?", resolvedUsername)
	}

	var rows []BillingDailyRow
	err := tx.Group(dayExpr).Order("day desc").Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	userCounts, err := getBillingDailyUserCounts(startTimestamp, endTimestamp, modelName, channel, tokenName, username, email)
	if err != nil {
		return nil, err
	}
	mergeBillingDailyUserCounts(&rows, userCounts)
	return rows, nil
}

func getBillingDailyTargetRequestCounts(startTimestamp, endTimestamp int64, modelName string, channel int, tokenName, username, email string) (map[int64]int64, error) {
	dayExpr := billingDayExpr("created_at")
	tx := LOG_DB.Table("logs").
		Select(dayExpr+" as day, COUNT(*) as accounting_target_request_count").
		Where("type = ? AND quota > 0 AND accounting_status <> ''", LogTypeConsume)

	if startTimestamp != 0 {
		tx = tx.Where("created_at >= ?", startTimestamp)
	}
	if endTimestamp != 0 {
		tx = tx.Where("created_at <= ?", endTimestamp)
	}
	if modelName != "" {
		tx = tx.Where("model_name = ?", modelName)
	}
	if channel != 0 {
		tx = tx.Where("channel_id = ?", channel)
	}
	if tokenName != "" {
		tx = tx.Where("token_name = ?", tokenName)
	}
	if username != "" {
		tx = tx.Where("username = ?", username)
	}
	if email != "" {
		var resolvedUsername string
		err := DB.Table("users").Select("username").Where("email = ?", email).Limit(1).Scan(&resolvedUsername).Error
		if err != nil {
			return nil, err
		}
		if resolvedUsername == "" {
			return map[int64]int64{}, nil
		}
		tx = tx.Where("username = ?", resolvedUsername)
	}

	var rows []billingDailyCountRow
	if err := tx.Group(dayExpr).Scan(&rows).Error; err != nil {
		return nil, err
	}
	counts := make(map[int64]int64, len(rows))
	for _, row := range rows {
		counts[row.Day] = row.AccountingTargetReqCount
	}
	return counts, nil
}

func mergeBillingDailyTargetRequestCounts(rows *[]BillingDailyRow, counts map[int64]int64) {
	if rows == nil {
		return
	}
	byDay := make(map[int64]*BillingDailyRow, len(*rows))
	for i := range *rows {
		row := &(*rows)[i]
		row.AccountingTargetReqCount = counts[row.Day]
		byDay[row.Day] = row
	}
	for day, count := range counts {
		if _, ok := byDay[day]; ok {
			continue
		}
		*rows = append(*rows, BillingDailyRow{
			Day:                      day,
			AccountingTargetReqCount: count,
		})
	}
	sort.Slice(*rows, func(i, j int) bool {
		return (*rows)[i].Day > (*rows)[j].Day
	})
}

func getBillingDailyUserCounts(startTimestamp, endTimestamp int64, modelName string, channel int, tokenName, username, email string) (map[int64]billingDailyUserCountRow, error) {
	dayExpr := billingDayExpr("created_at")
	tx, err := buildBillingDailyUserCountsBaseQuery(startTimestamp, endTimestamp, modelName, channel, tokenName, username, email)
	if err != nil {
		return nil, err
	}
	var rows []billingDailyUserCountRow
	if err := tx.
		Select(dayExpr + ` as day,
			COUNT(DISTINCT CASE WHEN quota > 0 AND accounting_status = 'ok' AND other NOT LIKE '%"billing_source":"subscription"%' THEN user_id ELSE NULL END) as non_subscription_user_count,
			COUNT(DISTINCT CASE WHEN quota > 0 AND accounting_status = 'ok' AND other LIKE '%"billing_source":"subscription"%' THEN user_id ELSE NULL END) as subscription_user_count`).
		Group(dayExpr).
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	counts := make(map[int64]billingDailyUserCountRow, len(rows))
	for _, row := range rows {
		counts[row.Day] = row
	}
	return counts, nil
}

func mergeBillingDailyUserCounts(rows *[]BillingDailyRow, counts map[int64]billingDailyUserCountRow) {
	if rows == nil {
		return
	}
	byDay := make(map[int64]*BillingDailyRow, len(*rows))
	for i := range *rows {
		row := &(*rows)[i]
		if count, ok := counts[row.Day]; ok {
			row.NonSubscriptionUserCount = count.NonSubscriptionUserCount
			row.SubscriptionUserCount = count.SubscriptionUserCount
		}
		byDay[row.Day] = row
	}
	for day, count := range counts {
		if _, ok := byDay[day]; ok {
			continue
		}
		*rows = append(*rows, BillingDailyRow{
			Day:                      day,
			NonSubscriptionUserCount: count.NonSubscriptionUserCount,
			SubscriptionUserCount:    count.SubscriptionUserCount,
		})
	}
	sort.Slice(*rows, func(i, j int) bool {
		return (*rows)[i].Day > (*rows)[j].Day
	})
}

func GetBillingUserCountsTotal(startTimestamp, endTimestamp int64, modelName string, channel int, tokenName, username, email string) (billingUserCountTotals, error) {
	tx, err := buildBillingDailyUserCountsBaseQuery(startTimestamp, endTimestamp, modelName, channel, tokenName, username, email)
	if err != nil {
		return billingUserCountTotals{}, err
	}
	var totals billingUserCountTotals
	if err := tx.
		Select(`COUNT(DISTINCT CASE WHEN quota > 0 AND accounting_status = 'ok' AND other NOT LIKE '%"billing_source":"subscription"%' THEN user_id ELSE NULL END) as non_subscription_user_count,
			COUNT(DISTINCT CASE WHEN quota > 0 AND accounting_status = 'ok' AND other LIKE '%"billing_source":"subscription"%' THEN user_id ELSE NULL END) as subscription_user_count`).
		Scan(&totals).Error; err != nil {
		return billingUserCountTotals{}, err
	}
	return totals, nil
}

func buildBillingDailyUserCountsBaseQuery(startTimestamp, endTimestamp int64, modelName string, channel int, tokenName, username, email string) (*gorm.DB, error) {
	tx := LOG_DB.Table("logs").Where("type = ?", LogTypeConsume)
	if startTimestamp != 0 {
		tx = tx.Where("created_at >= ?", startTimestamp)
	}
	if endTimestamp != 0 {
		tx = tx.Where("created_at <= ?", endTimestamp)
	}
	if modelName != "" {
		tx = tx.Where("model_name = ?", modelName)
	}
	if channel != 0 {
		tx = tx.Where("channel_id = ?", channel)
	}
	if tokenName != "" {
		tx = tx.Where("token_name = ?", tokenName)
	}
	if username != "" {
		tx = tx.Where("username = ?", username)
	}
	if email != "" {
		var resolvedUsername string
		err := DB.Table("users").Select("username").Where("email = ?", email).Limit(1).Scan(&resolvedUsername).Error
		if err != nil {
			return nil, err
		}
		if resolvedUsername == "" {
			return LOG_DB.Table("logs").Where("1 = 0"), nil
		}
		tx = tx.Where("username = ?", resolvedUsername)
	}
	return tx, nil
}

// GetNonAdminWalletBalanceUSD returns the current wallet liability for regular
// users. Subscription balances live in user_subscriptions and are deliberately
// excluded; GORM also excludes soft-deleted users from this query.
func GetNonAdminWalletBalanceUSD() (float64, error) {
	var totalQuota int64
	err := DB.Model(&User{}).
		Where("role < ?", common.RoleAdminUser).
		Select("COALESCE(SUM(quota), 0)").
		Scan(&totalQuota).Error
	if err != nil {
		return 0, err
	}
	if common.QuotaPerUnit <= 0 {
		return 0, nil
	}
	return float64(totalQuota) / common.QuotaPerUnit, nil
}

// GetNonAdminSubscriptionBalanceUSD returns the current remaining subscription
// liability for regular users, counting only active, unexpired subscriptions.
func GetNonAdminSubscriptionBalanceUSD() (float64, error) {
	now := common.GetTimestamp()
	var totalQuota int64
	err := DB.Model(&UserSubscription{}).
		Joins("JOIN users ON users.id = user_subscriptions.user_id AND users.deleted_at IS NULL").
		Where("users.role < ?", common.RoleAdminUser).
		Where("user_subscriptions.status = ? AND user_subscriptions.end_time > ?", "active", now).
		Select("COALESCE(SUM(CASE WHEN user_subscriptions.amount_total > user_subscriptions.amount_used THEN user_subscriptions.amount_total - user_subscriptions.amount_used ELSE 0 END), 0)").
		Scan(&totalQuota).Error
	if err != nil {
		return 0, err
	}
	if common.QuotaPerUnit <= 0 {
		return 0, nil
	}
	return float64(totalQuota) / common.QuotaPerUnit, nil
}

func UpsertBillingWalletDailySnapshot(day, snapshotAt int64) (float64, error) {
	balanceUSD, err := GetNonAdminWalletBalanceUSD()
	if err != nil {
		return 0, err
	}
	row := BillingWalletDailySnapshot{
		Day:              day,
		WalletBalanceUSD: balanceUSD,
		SnapshotAt:       snapshotAt,
	}
	err = DB.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "day"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"wallet_balance_usd", "snapshot_at",
		}),
	}).Create(&row).Error
	return balanceUSD, err
}

func UpsertBillingSubscriptionDailySnapshot(day, snapshotAt int64) (float64, error) {
	balanceUSD, err := GetNonAdminSubscriptionBalanceUSD()
	if err != nil {
		return 0, err
	}
	row := BillingSubscriptionDailySnapshot{
		Day:                    day,
		SubscriptionBalanceUSD: balanceUSD,
		SnapshotAt:             snapshotAt,
	}
	err = DB.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "day"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"subscription_balance_usd", "snapshot_at",
		}),
	}).Create(&row).Error
	return balanceUSD, err
}

func GetBillingWalletDailySnapshots(startTimestamp, endTimestamp int64) (map[int64]float64, error) {
	tx := DB.Model(&BillingWalletDailySnapshot{})
	if startTimestamp != 0 {
		tx = tx.Where("day >= ?", startTimestamp)
	}
	if endTimestamp != 0 {
		tx = tx.Where("day <= ?", endTimestamp)
	}
	var rows []BillingWalletDailySnapshot
	if err := tx.Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make(map[int64]float64, len(rows))
	for _, row := range rows {
		result[row.Day] = row.WalletBalanceUSD
	}
	return result, nil
}

func GetBillingSubscriptionDailySnapshots(startTimestamp, endTimestamp int64) (map[int64]float64, error) {
	tx := DB.Model(&BillingSubscriptionDailySnapshot{})
	if startTimestamp != 0 {
		tx = tx.Where("day >= ?", startTimestamp)
	}
	if endTimestamp != 0 {
		tx = tx.Where("day <= ?", endTimestamp)
	}
	var rows []BillingSubscriptionDailySnapshot
	if err := tx.Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make(map[int64]float64, len(rows))
	for _, row := range rows {
		result[row.Day] = row.SubscriptionBalanceUSD
	}
	return result, nil
}
