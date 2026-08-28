package service

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/bytedance/gopkg/util/gopool"
)

const (
	billingSummaryInterval = 1 * time.Hour
	// Re-aggregate a rolling lookback window rather than only "since last run",
	// so late-arriving/updated accounting rows still get folded in. Idempotent
	// via the OnConflict upsert in model.UpsertBillingHourlySummaries.
	billingSummaryLookback = 26 * time.Hour
	// Beijing uses UTC+8 and does not observe DST. The billing page and usage
	// logs both treat this as the canonical day boundary.
	billingDayTZOffsetSeconds = 8 * 3600
)

var billingSummaryOnce sync.Once
var billingSummaryNow = time.Now

type billingUserCountTotals struct {
	WalletUserCount           int64 `json:"wallet_user_count"`
	ExperienceUserCount       int64 `json:"experience_user_count"`
	PaidSubscriptionUserCount int64 `json:"paid_subscription_user_count"`
	CodingPlanUserCount       int64 `json:"coding_plan_user_count"`
}

func billingSummaryEffectiveEndTimestamp(endTimestamp int64) int64 {
	now := billingSummaryNow().Unix()
	if endTimestamp == 0 || endTimestamp > now {
		return now
	}
	return endTimestamp
}

func billingHourExpr(col string) string {
	if common.UsingMySQL {
		return fmt.Sprintf("(%s DIV 3600) * 3600", col)
	}
	return fmt.Sprintf("(%s / 3600) * 3600", col)
}

func billingDayStart(unixSeconds int64) int64 {
	return ((unixSeconds + billingDayTZOffsetSeconds) / 86400 * 86400) - billingDayTZOffsetSeconds
}

// StartBillingSummaryTask starts the hourly job that rolls Log accounting
// fields up into billing_hourly_summaries, backing the 平台账单 admin page.
func StartBillingSummaryTask() {
	billingSummaryOnce.Do(func() {
		if !common.IsMasterNode {
			return
		}
		gopool.Go(func() {
			logger.LogInfo(context.Background(), "billing-summary task started")
			ticker := time.NewTicker(billingSummaryInterval)
			defer ticker.Stop()
			runBillingSummaryOnce()
			for range ticker.C {
				runBillingSummaryOnce()
			}
		})
	})
}

func runBillingSummaryOnce() {
	ctx := context.Background()
	now := billingSummaryNow().Unix()
	if _, err := model.UpsertBillingWalletDailySnapshot(billingDayStart(now), now); err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("billing-summary: wallet snapshot failed: %v", err))
	}
	if _, err := model.UpsertBillingSubscriptionDailySnapshot(billingDayStart(now), now); err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("billing-summary: subscription snapshot failed: %v", err))
	}
	if _, err := model.UpsertBillingExperienceDailySnapshot(billingDayStart(now), now); err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("billing-summary: experience snapshot failed: %v", err))
	}
	if _, err := model.UpsertBillingPaidSubscriptionDailySnapshot(billingDayStart(now), now); err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("billing-summary: paid subscription snapshot failed: %v", err))
	}
	if _, err := model.UpsertBillingCodingPlanDailySnapshot(billingDayStart(now), now); err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("billing-summary: coding plan snapshot failed: %v", err))
	}
	// Floor to the hour: the upsert overwrites whole (hour, model, channel)
	// buckets, so the window boundary must sit exactly on a bucket edge. A
	// mid-hour boundary re-aggregates the straddled bucket from only part of
	// its rows and clobbers the previously complete value (found 2026-07-10:
	// every bucket lost its pre-boundary slice ~26h after its hour).
	since := billingSummaryNow().Add(-billingSummaryLookback).Unix() / 3600 * 3600
	hourExpr := billingHourExpr("created_at")

	var rows []model.BillingHourlySummary
	err := model.LOG_DB.Table("logs").
		Select(hourExpr+` as hour_bucket,
		         model_name,
		         channel_id,
		         SUM(CASE WHEN accounting_status = 'ok' THEN accounting_channel_cost_amount_usd ELSE 0 END) as cost_usd,
		         SUM(CASE WHEN accounting_status = 'ok' THEN CASE WHEN other LIKE '%"billing_source":"subscription"%' THEN quota * 1.0 / `+fmt.Sprintf("%v", common.QuotaPerUnit)+` ELSE accounting_user_final_amount_usd END ELSE 0 END) as revenue_usd,
		         SUM(CASE WHEN accounting_status = 'ok' AND other LIKE '%"billing_source":"subscription"%' THEN accounting_channel_cost_amount_usd ELSE 0 END) as subscription_cost_usd,
		         SUM(CASE WHEN accounting_status = 'ok' AND other LIKE '%"billing_source":"subscription"%' THEN quota * 1.0 / `+fmt.Sprintf("%v", common.QuotaPerUnit)+` ELSE 0 END) as subscription_billing_usd,
		         SUM(CASE WHEN accounting_status = 'ok' AND other LIKE '%"subscription_type":"gpt_subscription"%' THEN accounting_channel_cost_amount_usd ELSE 0 END) as paid_subscription_cost_usd,
		         SUM(CASE WHEN accounting_status = 'ok' AND other LIKE '%"subscription_type":"gpt_subscription"%' THEN quota * 1.0 / `+fmt.Sprintf("%v", common.QuotaPerUnit)+` ELSE 0 END) as paid_subscription_revenue_usd,
		         SUM(CASE WHEN accounting_status = 'ok' AND other LIKE '%"subscription_type":"coding_plan"%' THEN accounting_channel_cost_amount_usd ELSE 0 END) as coding_plan_cost_usd,
		         SUM(CASE WHEN accounting_status = 'ok' AND other LIKE '%"subscription_type":"coding_plan"%' THEN quota * 1.0 / `+fmt.Sprintf("%v", common.QuotaPerUnit)+` ELSE 0 END) as coding_plan_revenue_usd,
		         SUM(CASE WHEN accounting_status = 'ok' THEN 1 ELSE 0 END) as request_count`).
		Where("type = ? AND quota > 0 AND accounting_status <> '' AND created_at >= ?", model.LogTypeConsume, since).
		Group(hourExpr + ", model_name, channel_id").
		Scan(&rows).Error
	if err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("billing-summary: aggregate failed: %v", err))
		return
	}
	for i := range rows {
		rows[i].UpdatedAt = now
	}
	if err := model.UpsertBillingHourlySummaries(rows); err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("billing-summary: upsert failed: %v", err))
		return
	}
	logger.LogInfo(ctx, fmt.Sprintf("billing-summary: refreshed %d bucket rows since %d", len(rows), since))
}

func applyPaidSubscriptionAccruals(rows *[]model.BillingDailyRow, accruals map[int64]model.BillingPaidSubscriptionDailyAccrual) {
	if rows == nil {
		return
	}
	byDay := make(map[int64]*model.BillingDailyRow, len(*rows))
	for i := range *rows {
		row := &(*rows)[i]
		if accrual, ok := accruals[row.Day]; ok {
			row.RevenueUSD += accrual.RevenueUSD - row.PaidSubscriptionRevenueUSD
			if row.RevenueUSD < 0 {
				row.RevenueUSD = 0
			}
			row.PaidSubscriptionRevenueUSD = accrual.RevenueUSD
			row.PaidSubscriptionUserCount = accrual.UserCount
		} else {
			row.RevenueUSD -= row.PaidSubscriptionRevenueUSD
			if row.RevenueUSD < 0 {
				row.RevenueUSD = 0
			}
			row.PaidSubscriptionRevenueUSD = 0
			row.PaidSubscriptionUserCount = 0
		}
		byDay[row.Day] = row
	}
	for day, accrual := range accruals {
		if _, ok := byDay[day]; ok {
			continue
		}
		*rows = append(*rows, model.BillingDailyRow{
			Day:                        day,
			RevenueUSD:                 accrual.RevenueUSD,
			PaidSubscriptionRevenueUSD: accrual.RevenueUSD,
			PaidSubscriptionUserCount:  accrual.UserCount,
		})
	}
	sort.Slice(*rows, func(i, j int) bool {
		return (*rows)[i].Day > (*rows)[j].Day
	})
}

func applyCodingPlanExpiryAccounting(rows *[]model.BillingDailyRow, expiryByDay map[int64]model.BillingCodingPlanExpiryDaily) {
	if rows == nil {
		return
	}
	byDay := make(map[int64]*model.BillingDailyRow, len(*rows))
	for i := range *rows {
		row := &(*rows)[i]
		if expiry, ok := expiryByDay[row.Day]; ok {
			row.RevenueUSD += expiry.ExpiryRevenueUSD
			row.CodingPlanExpiredCount = expiry.ExpiredCount
			row.CodingPlanExpiredAllowanceUSD = expiry.ExpiredAllowanceUSD
			row.CodingPlanExpiryRevenueUSD = expiry.ExpiryRevenueUSD
		}
		byDay[row.Day] = row
	}
	for day, expiry := range expiryByDay {
		if _, ok := byDay[day]; ok {
			continue
		}
		*rows = append(*rows, model.BillingDailyRow{
			Day:                           day,
			RevenueUSD:                    expiry.ExpiryRevenueUSD,
			CodingPlanExpiredCount:        expiry.ExpiredCount,
			CodingPlanExpiredAllowanceUSD: expiry.ExpiredAllowanceUSD,
			CodingPlanExpiryRevenueUSD:    expiry.ExpiryRevenueUSD,
		})
	}
	sort.Slice(*rows, func(i, j int) bool {
		return (*rows)[i].Day > (*rows)[j].Day
	})
}

// GetBillingDaily picks the summary-table path when no user-identifying
// filter is set, otherwise falls back to querying raw logs directly (see
// model.GetBillingDailyFromRawLogs for why no name→id resolution is needed).
func GetBillingDaily(startTimestamp, endTimestamp int64, modelName string, channel int, tokenName, username, email string) ([]model.BillingDailyRow, error) {
	var rows []model.BillingDailyRow
	var err error
	if tokenName != "" || username != "" || email != "" {
		rows, err = model.GetBillingDailyFromRawLogs(startTimestamp, endTimestamp, modelName, channel, tokenName, username, email)
	} else {
		rows, err = getBillingDailyHybrid(startTimestamp, endTimestamp, modelName, channel)
	}
	if err != nil {
		return nil, err
	}
	snapshots, err := model.GetBillingWalletDailySnapshots(startTimestamp, endTimestamp)
	if err != nil {
		return nil, err
	}
	for i := range rows {
		if balance, ok := snapshots[rows[i].Day]; ok {
			balanceCopy := balance
			rows[i].WalletBalanceUSD = &balanceCopy
		}
	}
	legacySubscriptionSnapshots, err := model.GetBillingSubscriptionDailySnapshots(startTimestamp, endTimestamp)
	if err != nil {
		return nil, err
	}
	experienceSnapshots, err := model.GetBillingExperienceDailySnapshots(startTimestamp, endTimestamp)
	if err != nil {
		return nil, err
	}
	paidSubscriptionSnapshots, err := model.GetBillingPaidSubscriptionDailySnapshots(startTimestamp, endTimestamp)
	if err != nil {
		return nil, err
	}
	codingPlanSnapshots, err := model.GetBillingCodingPlanDailySnapshots(startTimestamp, endTimestamp)
	if err != nil {
		return nil, err
	}
	paidSubscriptionAccruals, err := model.GetBillingPaidSubscriptionDailyAccrualsAt(startTimestamp, endTimestamp, billingSummaryNow().Unix())
	if err != nil {
		return nil, err
	}
	applyPaidSubscriptionAccruals(&rows, paidSubscriptionAccruals)
	// Expiry events have no model/channel/token attribution, so they only
	// belong in the unfiltered platform view.
	if modelName == "" && channel == 0 && tokenName == "" && username == "" && email == "" {
		expiryByDay, err := model.GetBillingCodingPlanExpiryDaily(startTimestamp, endTimestamp)
		if err != nil {
			return nil, err
		}
		applyCodingPlanExpiryAccounting(&rows, expiryByDay)
	}
	for i := range rows {
		if balance, ok := experienceSnapshots[rows[i].Day]; ok {
			balanceCopy := balance
			rows[i].ExperienceBalanceUSD = &balanceCopy
		}
		if balance, ok := paidSubscriptionSnapshots[rows[i].Day]; ok {
			balanceCopy := balance
			rows[i].PaidSubscriptionBalanceUSD = &balanceCopy
		}
		if balance, ok := codingPlanSnapshots[rows[i].Day]; ok {
			balanceCopy := balance
			rows[i].CodingPlanBalanceUSD = &balanceCopy
		}
		if rows[i].ExperienceBalanceUSD == nil {
			if legacyBalance, ok := legacySubscriptionSnapshots[rows[i].Day]; ok {
				derivedExperience := legacyBalance
				if paidBalance, hasPaid := paidSubscriptionSnapshots[rows[i].Day]; hasPaid {
					derivedExperience -= paidBalance
				}
				if codingBalance, hasCoding := codingPlanSnapshots[rows[i].Day]; hasCoding {
					derivedExperience -= codingBalance
				}
				if derivedExperience < 0 {
					derivedExperience = 0
				}
				balanceCopy := derivedExperience
				rows[i].ExperienceBalanceUSD = &balanceCopy
			}
		}
	}
	return rows, nil
}

func GetBillingUserCountsTotal(startTimestamp, endTimestamp int64, modelName string, channel int, tokenName, username, email string) (billingUserCountTotals, error) {
	nowUnix := billingSummaryNow().Unix()
	totals, err := model.GetBillingUserCountsTotalAt(startTimestamp, endTimestamp, modelName, channel, tokenName, username, email, nowUnix)
	if err != nil {
		return billingUserCountTotals{}, err
	}
	return billingUserCountTotals{
		WalletUserCount:           totals.WalletUserCount,
		ExperienceUserCount:       totals.ExperienceUserCount,
		PaidSubscriptionUserCount: totals.PaidSubscriptionUserCount,
		CodingPlanUserCount:       totals.CodingPlanUserCount,
	}, nil
}

type billingDailyRangePlan struct {
	summaryStart int64
	summaryEnd   int64
	useSummary   bool
	rawStart     int64
	rawEnd       int64
	useRaw       bool
}

func planBillingDailyHybridRange(startTimestamp, endTimestamp, nowUnix int64) billingDailyRangePlan {
	todayStart := billingDayStart(nowUnix)
	plan := billingDailyRangePlan{}

	if endTimestamp != 0 && endTimestamp < todayStart {
		plan.summaryStart = startTimestamp
		plan.summaryEnd = endTimestamp
		plan.useSummary = true
		return plan
	}

	if startTimestamp >= todayStart {
		plan.rawStart = startTimestamp
		plan.rawEnd = endTimestamp
		plan.useRaw = true
		return plan
	}

	if startTimestamp == 0 || startTimestamp < todayStart {
		summaryEnd := endTimestamp
		if summaryEnd == 0 || summaryEnd >= todayStart {
			summaryEnd = todayStart - 1
		}
		if startTimestamp == 0 || startTimestamp <= summaryEnd {
			plan.summaryStart = startTimestamp
			plan.summaryEnd = summaryEnd
			plan.useSummary = true
		}
	}

	if endTimestamp == 0 || endTimestamp >= todayStart {
		rawStart := startTimestamp
		if rawStart < todayStart {
			rawStart = todayStart
		}
		plan.rawStart = rawStart
		plan.rawEnd = endTimestamp
		plan.useRaw = true
	}

	return plan
}

// getBillingDailyHybrid keeps historical days on the hourly summary table for
// performance, but routes the current Beijing day directly through raw logs so
// the "OK / Requests" percentage stays on the same freshness window.
func getBillingDailyHybrid(startTimestamp, endTimestamp int64, modelName string, channel int) ([]model.BillingDailyRow, error) {
	plan := planBillingDailyHybridRange(startTimestamp, endTimestamp, billingSummaryNow().Unix())
	rows := make([]model.BillingDailyRow, 0, 8)

	if plan.useSummary {
		summaryRows, err := model.GetBillingDailyFromSummary(plan.summaryStart, plan.summaryEnd, modelName, channel)
		if err != nil {
			return nil, err
		}
		rows = append(rows, summaryRows...)
	}

	if plan.useRaw {
		rawRows, err := model.GetBillingDailyFromRawLogs(plan.rawStart, plan.rawEnd, modelName, channel, "", "", "")
		if err != nil {
			return nil, err
		}
		rows = append(rows, rawRows...)
	}

	sort.Slice(rows, func(i, j int) bool {
		return rows[i].Day > rows[j].Day
	})
	return rows, nil
}
