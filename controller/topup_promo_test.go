package controller

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestGetFirstTopupPromoReturnsConfiguredPayAmount(t *testing.T) {
	oldEnabled := common.FirstTopupPromoEnabled
	oldAmount := common.FirstTopupPromoAmount
	oldDiscount := common.FirstTopupPromoDiscount
	t.Cleanup(func() {
		common.FirstTopupPromoEnabled = oldEnabled
		common.FirstTopupPromoAmount = oldAmount
		common.FirstTopupPromoDiscount = oldDiscount
	})

	common.FirstTopupPromoEnabled = true
	common.FirstTopupPromoAmount = 10
	common.FirstTopupPromoDiscount = 0.85

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	GetFirstTopupPromo(context)

	require.Equal(t, 200, recorder.Code)
	var response struct {
		Success bool `json:"success"`
		Data    struct {
			Amount    int     `json:"amount"`
			Discount  float64 `json:"discount"`
			PayAmount float64 `json:"pay_amount"`
			Eligible  bool    `json:"eligible"`
			NeverPaid bool    `json:"never_recharged"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.True(t, response.Success)
	require.Equal(t, 10, response.Data.Amount)
	require.InDelta(t, 0.85, response.Data.Discount, 0.000001)
	require.InDelta(t, 8.5, response.Data.PayAmount, 0.000001)
	require.True(t, response.Data.Eligible)
	require.True(t, response.Data.NeverPaid)
}
