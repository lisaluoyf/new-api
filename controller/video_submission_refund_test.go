package controller

import (
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestOriginalVideoRefundLookupByTaskAndKey(t *testing.T) {
	old := model.DB
	t.Cleanup(func() { model.DB = old })
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	require.NoError(t, db.AutoMigrate(&model.Task{}, &model.VideoSubmission{}, &model.VideoSubmissionRefund{}, &model.BillingHold{}, &model.SeedanceBillingReceipt{}))
	key := "original-customer-key"
	row := model.VideoSubmission{ID: service.VideoSubmissionID(7, key), UserID: 7, TaskID: "task_refunded_original", RequestID: "request-original", Model: "seedance-2.5", Status: model.VideoSubmissionUnknown}
	require.NoError(t, db.Create(&row).Error)
	require.NoError(t, db.Create(&model.BillingHold{RequestId: row.RequestID, UserId: 7, Status: model.BillingHoldStatusRefunded, PreConsumedQuota: 1646589, ResolvedAt: 1234}).Error)
	for _, uid := range []int{7, 8, 7} {
		for _, byKey := range []bool{false, true} {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Set("id", uid)
			c.Request = httptest.NewRequest("GET", "/", nil)
			if byKey {
				// A stranger has an independent, absent key claim; avoid legacy fallback.
				if uid != 7 {
					continue
				}
				c.Request.Header.Set("Idempotency-Key", key)
				GetVideoSubmissionByKey(c)
			} else {
				c.Params = gin.Params{{Key: "task_id", Value: row.TaskID}}
				GetSeedanceBillingReceipt(c)
			}
			if uid != 7 {
				require.Equal(t, 404, w.Code)
				require.NotContains(t, w.Body.String(), "3.293178")
				continue
			}
			require.Equal(t, 200, w.Code)
			var body map[string]any
			require.NoError(t, common.Unmarshal(w.Body.Bytes(), &body))
			require.Contains(t, w.Body.String(), `"refunded_amount":"3.293178"`)
			require.Contains(t, w.Body.String(), `"new_submission_safe":false`)
			require.Contains(t, w.Body.String(), `"submission_status":"acceptance_unknown"`)
			require.NotContains(t, w.Body.String(), "recovery_json")
		}
	}
	var count int64
	require.NoError(t, db.Model(&model.VideoSubmissionRefund{}).Count(&count).Error)
	require.Zero(t, count, "historical query must not create a refund or move money")
}
