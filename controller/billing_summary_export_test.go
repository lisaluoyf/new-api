package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildBillingSummaryExportRows(t *testing.T) {
	walletBalance := 42.5
	experienceBalance := 18.25
	paidSubscriptionBalance := 9.75
	rows := buildBillingSummaryExportRows([]model.BillingDailyRow{
		{
			Day:                        1_783_785_600, // 2026-07-12 00:00:00 Asia/Shanghai
			CostUSD:                    5,
			RevenueUSD:                 11,
			ExperienceCostUSD:          2,
			ExperienceBillingUSD:       4,
			PaidSubscriptionCostUSD:    1,
			PaidSubscriptionRevenueUSD: 1.5,
			AccountingOKRequestCount:   8,
			AccountingTargetReqCount:   10,
			WalletUserCount:            6,
			ExperienceUserCount:        2,
			PaidSubscriptionUserCount:  1,
			WalletBalanceUSD:           &walletBalance,
			ExperienceBalanceUSD:       &experienceBalance,
			PaidSubscriptionBalanceUSD: &paidSubscriptionBalance,
		},
		{
			Day:                  1_783_699_200,
			CostUSD:              2,
			RevenueUSD:           3,
			ExperienceCostUSD:    2,
			ExperienceBillingUSD: 3,
		},
	})

	require.Len(t, rows, 2)
	assert.Equal(t, "2026-07-12", rows[0].Date)
	assert.InDelta(t, 2, rows[0].WalletCostUSD, 1e-9)
	assert.InDelta(t, 5.5, rows[0].WalletRevenueUSD, 1e-9)
	assert.InDelta(t, 3.5, rows[0].WalletProfitUSD, 1e-9)
	require.NotNil(t, rows[0].WalletMarginPercent)
	assert.InDelta(t, 175, *rows[0].WalletMarginPercent, 1e-9)
	assert.Equal(t, int64(6), rows[0].WalletUserCount)
	assert.Equal(t, int64(1), rows[0].PaidSubscriptionUserCount)
	assert.Equal(t, int64(8), rows[0].AccountingOKRequestCount)
	assert.Equal(t, int64(10), rows[0].AccountingTargetRequestCount)
	require.NotNil(t, rows[0].WalletBalanceUSD)
	assert.InDelta(t, 42.5, *rows[0].WalletBalanceUSD, 1e-9)
	require.NotNil(t, rows[0].ExperienceBalanceUSD)
	assert.InDelta(t, 18.25, *rows[0].ExperienceBalanceUSD, 1e-9)
	require.NotNil(t, rows[0].PaidSubscriptionBalanceUSD)
	assert.InDelta(t, 9.75, *rows[0].PaidSubscriptionBalanceUSD, 1e-9)
	assert.Nil(t, rows[1].WalletMarginPercent)
}

func TestBillingSummaryExportRequiresSecret(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("disabled", func(t *testing.T) {
		t.Setenv("BILLING_EXPORT_SECRET", "")
		t.Setenv("CATALOG_EXPORT_SECRET", "")
		response := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(response)
		c.Request = httptest.NewRequest(http.MethodGet, "/api/internal/billing-summary-export", nil)

		BillingSummaryExport(c)

		assert.Equal(t, http.StatusNotFound, response.Code)
	})

	t.Run("invalid", func(t *testing.T) {
		t.Setenv("BILLING_EXPORT_SECRET", "expected-secret")
		t.Setenv("CATALOG_EXPORT_SECRET", "fallback-secret")
		response := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(response)
		c.Request = httptest.NewRequest(http.MethodGet, "/api/internal/billing-summary-export", nil)
		c.Request.Header.Set("X-Billing-Export-Secret", "wrong-secret")

		BillingSummaryExport(c)

		assert.Equal(t, http.StatusForbidden, response.Code)
	})
}

func TestBillingExportCredentialsFallsBackToCatalogSecret(t *testing.T) {
	t.Setenv("BILLING_EXPORT_SECRET", "")
	t.Setenv("CATALOG_EXPORT_SECRET", "catalog-secret")
	response := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(response)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/internal/billing-summary-export", nil)
	c.Request.Header.Set("X-Catalog-Export-Secret", "catalog-secret")

	secret, provided := billingExportCredentials(c)

	assert.Equal(t, "catalog-secret", secret)
	assert.Equal(t, "catalog-secret", provided)
}
