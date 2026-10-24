package model

import (
	"fmt"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// postgresBillingRollupInput keeps large log text out of the aggregate's sort.
// Concatenation detoasts other once; the first OFFSET 0 prevents the planner
// from substituting that expression into every LIKE. The second keeps the
// computed flags below the aggregate, so repeated conditions reuse them.
// These streaming subqueries work on PostgreSQL 9.6+, without a materialized
// copy of the large text or any session/global planner setting changes.
// All expressions passed here are internal SQL, never request parameters.
func postgresBillingRollupInput(db, scope *gorm.DB, columns, flags, filter string) *gorm.DB {
	decoded := scope.Select(columns + ", other || '' AS other")
	classified := db.Table("(? OFFSET 0) AS billing_decoded", decoded).
		Select(columns + ", " + flags)
	if filter != "" {
		classified = classified.Where(filter)
	}
	return db.Table("(? OFFSET 0) AS billing_input", classified)
}

func billingHourlySummaryQuery(db *gorm.DB, since int64) *gorm.DB {
	hourExpr := "(created_at / 3600) * 3600"
	if db.Dialector.Name() == "mysql" {
		hourExpr = "(created_at DIV 3600) * 3600"
	}
	scope := db.Table("logs").Where("type = ? AND quota > 0 AND accounting_status <> '' AND created_at >= ?", LogTypeConsume, since)
	subscription := `other LIKE '%"billing_source":"subscription"%'`
	paidSubscription := `other LIKE '%"subscription_type":"gpt_subscription"%'`
	codingPlan := `other LIKE '%"subscription_type":"coding_plan"%'`
	if db.Dialector.Name() == "postgres" {
		scope = postgresBillingRollupInput(db, scope,
			"created_at, model_name, channel_id, quota, wallet_supplement_quota, accounting_status, accounting_channel_cost_amount_usd, accounting_user_final_amount_usd",
			subscription+" AS billing_is_subscription, "+paidSubscription+" AS billing_is_paid_subscription, "+codingPlan+" AS billing_is_coding_plan", "")
		subscription, paidSubscription, codingPlan = "billing_is_subscription", "billing_is_paid_subscription", "billing_is_coding_plan"
	}
	// Retain the legacy predicates (including LIKE and NULL semantics), decimal
	// arithmetic, and non-OK rows that clear previously charged/refunded buckets.
	return scope.Select(fmt.Sprintf(`%s as hour_bucket,
		model_name, channel_id,
		SUM(CASE WHEN accounting_status = 'ok' THEN accounting_channel_cost_amount_usd ELSE 0 END) as cost_usd,
		SUM(CASE WHEN accounting_status = 'ok' THEN CASE WHEN %s THEN quota * 1.0 / %[5]v ELSE accounting_user_final_amount_usd END ELSE 0 END) as revenue_usd,
		SUM(CASE WHEN accounting_status = 'ok' AND %[2]s THEN accounting_channel_cost_amount_usd * (quota - COALESCE(wallet_supplement_quota, 0)) * 1.0 / quota ELSE 0 END) as subscription_cost_usd,
		SUM(CASE WHEN accounting_status = 'ok' AND %[2]s THEN (quota - COALESCE(wallet_supplement_quota, 0)) * 1.0 / %[5]v ELSE 0 END) as subscription_billing_usd,
		SUM(CASE WHEN accounting_status = 'ok' AND %[3]s THEN accounting_channel_cost_amount_usd ELSE 0 END) as paid_subscription_cost_usd,
		SUM(CASE WHEN accounting_status = 'ok' AND %[3]s THEN quota * 1.0 / %[5]v ELSE 0 END) as paid_subscription_revenue_usd,
		SUM(CASE WHEN accounting_status = 'ok' AND %[4]s THEN accounting_channel_cost_amount_usd ELSE 0 END) as coding_plan_cost_usd,
		SUM(CASE WHEN accounting_status = 'ok' AND %[4]s THEN quota * 1.0 / %[5]v ELSE 0 END) as coding_plan_revenue_usd,
		SUM(CASE WHEN accounting_status = 'ok' THEN 1 ELSE 0 END) as request_count,
		COUNT(*) as accounting_target_request_count`, hourExpr, subscription, paidSubscription, codingPlan, common.QuotaPerUnit)).
		Group(hourExpr + ", model_name, channel_id")
}

// BuildBillingHourlySummaries preserves the rolling window's whole-hour buckets.
func BuildBillingHourlySummaries(since int64) ([]BillingHourlySummary, error) {
	var rows []BillingHourlySummary
	err := billingHourlySummaryQuery(LOG_DB, since).Scan(&rows).Error
	return rows, err
}

func billingDailyUserActivityQuery(db *gorm.DB, day int64) *gorm.DB {
	dayExpr := billingDayExpr("created_at")
	scope := db.Table("logs").Where("type = ? AND quota > 0 AND created_at >= ? AND created_at < ?", LogTypeConsume, day, day+86400)
	wallet, experience := billingWalletCondition(), billingExperienceSubscriptionCondition()
	paidSubscription, codingPlan := billingGPTSubscriptionCondition(), billingCodingPlanCondition()
	if db.Dialector.Name() == "postgres" {
		// Filter by date before accounting_status. Without column statistics the
		// planner can otherwise scan millions of historical 'ok' index entries.
		scope = postgresBillingRollupInput(db, scope,
			"created_at, model_name, channel_id, user_id, wallet_supplement_quota, accounting_status",
			`COALESCE(other, '') LIKE '%"billing_source":"wallet"%' AS billing_is_wallet,
			 COALESCE(other, '') LIKE '%"billing_source":"subscription"%' AS billing_is_subscription,
			 COALESCE(other, '') LIKE '%"subscription_type":"gpt_trial"%' AS billing_is_trial,
			 COALESCE(other, '') LIKE '%"subscription_type":"gpt_referral_reward"%' AS billing_is_referral,
			 COALESCE(other, '') LIKE '%"subscription_type":"gpt_subscription"%' AS billing_is_paid_subscription,
			 COALESCE(other, '') LIKE '%"subscription_type":"coding_plan"%' AS billing_is_coding_plan`, "accounting_status = 'ok'")
		experience = "(billing_is_trial OR billing_is_referral)"
		paidSubscription, codingPlan = "billing_is_paid_subscription", "billing_is_coding_plan"
		wallet = "(COALESCE(wallet_supplement_quota, 0) > 0 OR billing_is_wallet OR (NOT billing_is_subscription AND NOT " + experience + " AND NOT (" + paidSubscription + " OR " + codingPlan + ")))"
	} else {
		scope = scope.Where("accounting_status = 'ok'")
	}
	return scope.Select(dayExpr + ` as day, model_name, channel_id, user_id,
		MAX(CASE WHEN ` + wallet + ` THEN 1 ELSE 0 END) as wallet,
		MAX(CASE WHEN ` + experience + ` THEN 1 ELSE 0 END) as experience,
		MAX(CASE WHEN ` + paidSubscription + ` THEN 1 ELSE 0 END) as paid_subscription,
		MAX(CASE WHEN ` + codingPlan + ` THEN 1 ELSE 0 END) as coding_plan`).
		Group(dayExpr + ", model_name, channel_id, user_id")
}
