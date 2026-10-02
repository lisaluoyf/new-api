package relay

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestSeedanceBillingIsInjectedAfterAnyProviderConverter(t *testing.T) {
	gin.SetMode(gin.TestMode)
	oldDB := model.DB
	t.Cleanup(func() { model.DB = oldDB })
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	require.NoError(t, db.AutoMigrate(&model.SeedanceBillingReceipt{}))
	for _, name := range []string{"seedance-2.0", "seedance-2.5", "seedance-2.0-fast", "seedance-2.0-mini"} {
		task := &model.Task{UserId: 1, TaskID: "task_" + name, Status: model.TaskStatusSuccess, Properties: model.Properties{OriginModelName: name}, PrivateData: model.TaskPrivateData{SeedanceBillingReceiptEnabled: true}}
		details := model.SeedanceBillingDetails{Model: name, TaskID: task.TaskID, ReceiptID: "bill_" + task.TaskID, SettlementStatus: "settled"}
		raw, err := common.Marshal(details)
		require.NoError(t, err)
		require.NoError(t, db.Create(&model.SeedanceBillingReceipt{ID: details.ReceiptID, TaskID: task.TaskID, UserID: 1, Status: "settled", Details: raw}).Error)
		body, err := addSeedanceResultFieldsToResponse(task, []byte(`{"object":"video","status":"completed"}`))
		require.NoError(t, err)
		var response struct {
			Billing model.SeedanceBillingDetails `json:"billing"`
		}
		require.NoError(t, common.Unmarshal(body, &response))
		require.Equal(t, details.ReceiptID, response.Billing.ReceiptID)
		require.Equal(t, name, response.Billing.Model)
	}
}
