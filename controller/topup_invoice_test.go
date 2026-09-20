package controller

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestTopupInvoiceAccessAndPaymentStatus(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.TopUp{}, &model.SubscriptionOrder{}))
	previous := model.DB
	model.DB = db
	t.Cleanup(func() { model.DB = previous })
	order := model.TopUp{UserId: 7, TradeNo: "PAID-123", Money: 8.5, Amount: 10,
		PaymentMethod: "stripe", Status: "success", CompleteTime: 1780000000}
	require.NoError(t, db.Create(&order).Error)
	for _, test := range []struct {
		name         string
		userID, role int
		admin        bool
		id           string
		status       string
		money        float64
		want         int
	}{
		{"owner", 7, common.RoleCommonUser, false, fmt.Sprint(order.Id), "success", 8.5, 200},
		{"other customer", 8, common.RoleCommonUser, false, fmt.Sprint(order.Id), "success", 8.5, 404},
		{"anonymous", 0, 0, false, fmt.Sprint(order.Id), "success", 8.5, 401},
		{"admin", 8, common.RoleAdminUser, true, fmt.Sprint(order.Id), "success", 8.5, 200},
		{"non admin", 7, common.RoleCommonUser, true, fmt.Sprint(order.Id), "success", 8.5, 403},
		{"missing", 7, common.RoleCommonUser, false, "9999", "success", 8.5, 404},
		{"invalid id", 7, common.RoleCommonUser, false, "bad", "success", 8.5, 400},
		{"pending", 7, common.RoleCommonUser, false, fmt.Sprint(order.Id), "pending", 8.5, 409},
		{"expired", 7, common.RoleCommonUser, false, fmt.Sprint(order.Id), "expired", 8.5, 409},
		{"refunded", 7, common.RoleCommonUser, false, fmt.Sprint(order.Id), "refunded", 8.5, 409},
		{"free", 7, common.RoleCommonUser, false, fmt.Sprint(order.Id), "success", 0, 409},
	} {
		t.Run(test.name, func(t *testing.T) {
			require.NoError(t, db.Model(&order).Updates(map[string]interface{}{"status": test.status, "money": test.money}).Error)
			router := gin.New()
			router.GET("/invoice/:id", func(c *gin.Context) {
				c.Set("id", test.userID)
				c.Set("role", test.role)
				downloadTopupInvoice(c, test.admin)
			})
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/invoice/"+test.id, nil))
			require.Equal(t, test.want, response.Code)
			require.Equal(t, "private, no-store", response.Header().Get("Cache-Control"))
			if test.want == 200 {
				require.Equal(t, "application/pdf", response.Header().Get("Content-Type"))
				require.Contains(t, response.Header().Get("Content-Disposition"), "invoice-APIM-1.pdf")
				require.True(t, len(response.Body.Bytes()) > 1000)
				require.Equal(t, "%PDF-", response.Body.String()[:5])
			} else {
				require.NotContains(t, response.Body.String(), "PAID-123")
			}
		})
	}
}
