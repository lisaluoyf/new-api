package controller

import (
	"crypto/subtle"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

const billingExportTimezone = "Asia/Shanghai"

var billingExportLocation = time.FixedZone(billingExportTimezone, 8*60*60)

// billingSummaryExportRow mirrors all values shown on the Platform Billing
// page. Derived non-subscription values are materialized here so downstream
// services do not need to reproduce frontend-only formulas.
type billingSummaryExportRow struct {
	Day                          int64    `json:"day"`
	Date                         string   `json:"date"`
	TotalCostUSD                 float64  `json:"total_cost_usd"`
	TotalRevenueUSD              float64  `json:"total_revenue_usd"`
	NonSubscriptionCostUSD       float64  `json:"non_subscription_cost_usd"`
	NonSubscriptionRevenueUSD    float64  `json:"non_subscription_revenue_usd"`
	NonSubscriptionProfitUSD     float64  `json:"non_subscription_profit_usd"`
	NonSubscriptionMarginPercent *float64 `json:"non_subscription_margin_percent"`
	SubscriptionCostUSD          float64  `json:"subscription_cost_usd"`
	SubscriptionBillingUSD       float64  `json:"subscription_billing_usd"`
	NonSubscriptionUserCount     int64    `json:"non_subscription_user_count"`
	SubscriptionUserCount        int64    `json:"subscription_user_count"`
	WalletBalanceUSD             *float64 `json:"wallet_balance_usd"`
	SubscriptionBalanceUSD       *float64 `json:"subscription_balance_usd"`
	AccountingOKRequestCount     int64    `json:"accounting_ok_request_count"`
	AccountingTargetRequestCount int64    `json:"accounting_target_request_count"`
}

func buildBillingSummaryExportRows(rows []model.BillingDailyRow) []billingSummaryExportRow {
	exportRows := make([]billingSummaryExportRow, 0, len(rows))
	for _, row := range rows {
		nonSubscriptionCost := row.CostUSD - row.SubscriptionCostUSD
		nonSubscriptionRevenue := row.RevenueUSD - row.SubscriptionBillingUSD
		nonSubscriptionProfit := nonSubscriptionRevenue - nonSubscriptionCost

		var marginPercent *float64
		if nonSubscriptionCost > 0 {
			margin := nonSubscriptionProfit / nonSubscriptionCost * 100
			marginPercent = &margin
		}

		exportRows = append(exportRows, billingSummaryExportRow{
			Day:                          row.Day,
			Date:                         time.Unix(row.Day, 0).In(billingExportLocation).Format("2006-01-02"),
			TotalCostUSD:                 row.CostUSD,
			TotalRevenueUSD:              row.RevenueUSD,
			NonSubscriptionCostUSD:       nonSubscriptionCost,
			NonSubscriptionRevenueUSD:    nonSubscriptionRevenue,
			NonSubscriptionProfitUSD:     nonSubscriptionProfit,
			NonSubscriptionMarginPercent: marginPercent,
			SubscriptionCostUSD:          row.SubscriptionCostUSD,
			SubscriptionBillingUSD:       row.SubscriptionBillingUSD,
			NonSubscriptionUserCount:     row.NonSubscriptionUserCount,
			SubscriptionUserCount:        row.SubscriptionUserCount,
			WalletBalanceUSD:             row.WalletBalanceUSD,
			SubscriptionBalanceUSD:       row.SubscriptionBalanceUSD,
			AccountingOKRequestCount:     row.AccountingOKRequestCount,
			AccountingTargetRequestCount: row.AccountingTargetReqCount,
		})
	}
	return exportRows
}

func billingExportCredentials(c *gin.Context) (secret string, provided string) {
	secret = strings.TrimSpace(common.GetEnvOrDefaultString("BILLING_EXPORT_SECRET", ""))
	provided = strings.TrimSpace(c.GetHeader("X-Billing-Export-Secret"))
	if secret != "" {
		return secret, provided
	}
	secret = strings.TrimSpace(common.GetEnvOrDefaultString("CATALOG_EXPORT_SECRET", ""))
	if provided == "" {
		provided = strings.TrimSpace(c.GetHeader("X-Catalog-Export-Secret"))
	}
	return secret, provided
}

// BillingSummaryExport returns every available daily Platform Billing row,
// newest first. It is intended for service-to-service reads by Roma.
//
// Authentication prefers X-Billing-Export-Secret/BILLING_EXPORT_SECRET. When
// no dedicated secret is configured, Roma may reuse its existing
// X-Catalog-Export-Secret/CATALOG_EXPORT_SECRET credentials.
func BillingSummaryExport(c *gin.Context) {
	secret, provided := billingExportCredentials(c)
	if secret == "" {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "billing export is not enabled"})
		return
	}
	if subtle.ConstantTimeCompare([]byte(provided), []byte(secret)) != 1 {
		c.JSON(http.StatusForbidden, gin.H{"success": false, "message": "invalid secret"})
		return
	}

	rows, err := service.GetBillingDaily(0, 0, "", 0, "", "", "")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		return
	}
	userCounts, err := model.GetBillingUserCountsTotal(0, 0, "", 0, "", "", "")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		return
	}
	walletBalanceUSD, err := model.GetNonAdminWalletBalanceUSD()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		return
	}
	subscriptionBalanceUSD, err := model.GetNonAdminSubscriptionBalanceUSD()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"generated_at":                time.Now().Unix(),
			"timezone":                    billingExportTimezone,
			"currency":                    "USD",
			"wallet_balance_usd":          walletBalanceUSD,
			"subscription_balance_usd":    subscriptionBalanceUSD,
			"non_subscription_user_count": userCounts.NonSubscriptionUserCount,
			"subscription_user_count":     userCounts.SubscriptionUserCount,
			"rows":                        buildBillingSummaryExportRows(rows),
		},
	})
}
