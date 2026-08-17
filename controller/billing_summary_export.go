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
	WalletCostUSD                float64  `json:"wallet_cost_usd"`
	WalletRevenueUSD             float64  `json:"wallet_revenue_usd"`
	WalletProfitUSD              float64  `json:"wallet_profit_usd"`
	WalletMarginPercent          *float64 `json:"wallet_margin_percent"`
	ExperienceCostUSD            float64  `json:"experience_cost_usd"`
	ExperienceBillingUSD         float64  `json:"experience_billing_usd"`
	WalletUserCount              int64    `json:"wallet_user_count"`
	PaidSubscriptionCostUSD      float64  `json:"paid_subscription_cost_usd"`
	PaidSubscriptionRevenueUSD   float64  `json:"paid_subscription_revenue_usd"`
	PaidSubscriptionUserCount    int64    `json:"paid_subscription_user_count"`
	ExperienceUserCount          int64    `json:"experience_user_count"`
	WalletBalanceUSD             *float64 `json:"wallet_balance_usd"`
	ExperienceBalanceUSD         *float64 `json:"experience_balance_usd"`
	PaidSubscriptionBalanceUSD   *float64 `json:"paid_subscription_balance_usd"`
	AccountingOKRequestCount     int64    `json:"accounting_ok_request_count"`
	AccountingTargetRequestCount int64    `json:"accounting_target_request_count"`
}

func buildBillingSummaryExportRows(rows []model.BillingDailyRow) []billingSummaryExportRow {
	exportRows := make([]billingSummaryExportRow, 0, len(rows))
	for _, row := range rows {
		walletCost := row.CostUSD - row.ExperienceCostUSD - row.PaidSubscriptionCostUSD
		if walletCost < 0 {
			walletCost = 0
		}
		walletRevenue := row.RevenueUSD - row.ExperienceBillingUSD - row.PaidSubscriptionRevenueUSD
		if walletRevenue < 0 {
			walletRevenue = 0
		}
		walletProfit := walletRevenue - walletCost

		var walletMarginPercent *float64
		if walletCost > 0 {
			margin := walletProfit / walletCost * 100
			walletMarginPercent = &margin
		}

		exportRows = append(exportRows, billingSummaryExportRow{
			Day:                          row.Day,
			Date:                         time.Unix(row.Day, 0).In(billingExportLocation).Format("2006-01-02"),
			TotalCostUSD:                 row.CostUSD,
			TotalRevenueUSD:              row.RevenueUSD,
			WalletCostUSD:                walletCost,
			WalletRevenueUSD:             walletRevenue,
			WalletProfitUSD:              walletProfit,
			WalletMarginPercent:          walletMarginPercent,
			ExperienceCostUSD:            row.ExperienceCostUSD,
			ExperienceBillingUSD:         row.ExperienceBillingUSD,
			WalletUserCount:              row.WalletUserCount,
			PaidSubscriptionCostUSD:      row.PaidSubscriptionCostUSD,
			PaidSubscriptionRevenueUSD:   row.PaidSubscriptionRevenueUSD,
			PaidSubscriptionUserCount:    row.PaidSubscriptionUserCount,
			ExperienceUserCount:          row.ExperienceUserCount,
			WalletBalanceUSD:             row.WalletBalanceUSD,
			ExperienceBalanceUSD:         row.ExperienceBalanceUSD,
			PaidSubscriptionBalanceUSD:   row.PaidSubscriptionBalanceUSD,
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
	experienceBalanceUSD, err := model.GetNonAdminExperienceBalanceUSD()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		return
	}
	paidSubscriptionBalanceUSD, err := model.GetNonAdminPaidSubscriptionBalanceUSD()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"generated_at":                  time.Now().Unix(),
			"timezone":                      billingExportTimezone,
			"currency":                      "USD",
			"wallet_balance_usd":            walletBalanceUSD,
			"experience_balance_usd":        experienceBalanceUSD,
			"paid_subscription_balance_usd": paidSubscriptionBalanceUSD,
			"wallet_user_count":             userCounts.WalletUserCount,
			"experience_user_count":         userCounts.ExperienceUserCount,
			"paid_subscription_user_count":  userCounts.PaidSubscriptionUserCount,
			"rows":                          buildBillingSummaryExportRows(rows),
		},
	})
}
