package controller

import (
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestSeedanceReceiptEndpointOwnerIsolationAndStableRefundResponse(t *testing.T) {
	oldDB := model.DB
	t.Cleanup(func() { model.DB = oldDB })
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	require.NoError(t, db.AutoMigrate(&model.SeedanceBillingReceipt{}))
	net := "0.000000"
	details := model.SeedanceBillingDetails{ReceiptID: "bill_task_owned", TaskID: "task_owned", SettlementStatus: "refunded", NetAmount: &net, RefundedAmount: "0.200000", InputVideos: []model.SeedanceInputVideoMeasurement{}}
	raw, _ := common.Marshal(details)
	require.NoError(t, db.Create(&model.SeedanceBillingReceipt{ID: details.ReceiptID, TaskID: details.TaskID, UserID: 1, Status: "refunded", Details: raw}).Error)
	previous := ""
	for _, uid := range []int{1, 1, 2, 1} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Set("id", uid)
		c.Params = gin.Params{{Key: "task_id", Value: "task_owned"}}
		c.Request = httptest.NewRequest("GET", "/v1/videos/task_owned/billing", nil)
		GetSeedanceBillingReceipt(c)
		if uid == 2 {
			require.Equal(t, 404, w.Code)
			require.NotContains(t, w.Body.String(), "bill_task_owned")
		} else {
			require.Equal(t, 200, w.Code)
			if previous != "" {
				require.Equal(t, previous, w.Body.String())
			}
			previous = w.Body.String()
		}
	}
}
