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
	rows := buildBillingSummaryExportRows([]model.BillingDailyRow{
		{
			Day:                      1_783_785_600, // 2026-07-12 00:00:00 Asia/Shanghai
			CostUSD:                  5,
			RevenueUSD:               11,
			SubscriptionCostUSD:      2,
			SubscriptionBillingUSD:   4,
			AccountingOKRequestCount: 8,
			AccountingTargetReqCount: 10,
		},
		{
			Day:                    1_783_699_200,
			CostUSD:                2,
			RevenueUSD:             3,
			SubscriptionCostUSD:    2,
			SubscriptionBillingUSD: 3,
		},
	})

	require.Len(t, rows, 2)
	assert.Equal(t, "2026-07-12", rows[0].Date)
	assert.InDelta(t, 3, rows[0].NonSubscriptionCostUSD, 1e-9)
	assert.InDelta(t, 7, rows[0].NonSubscriptionRevenueUSD, 1e-9)
	assert.InDelta(t, 4, rows[0].NonSubscriptionProfitUSD, 1e-9)
	require.NotNil(t, rows[0].NonSubscriptionMarginPercent)
	assert.InDelta(t, 133.333333333, *rows[0].NonSubscriptionMarginPercent, 1e-9)
	assert.Equal(t, int64(8), rows[0].AccountingOKRequestCount)
	assert.Equal(t, int64(10), rows[0].AccountingTargetRequestCount)
	assert.Nil(t, rows[1].NonSubscriptionMarginPercent)
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
