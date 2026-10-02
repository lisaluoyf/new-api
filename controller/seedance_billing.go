package controller

import (
	"errors"
	"net/http"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func QuoteSeedanceVideo(c *gin.Context) {
	fields := map[string]any{}
	if err := common.UnmarshalBodyReusable(c, &fields); err != nil {
		c.JSON(400, gin.H{"error": gin.H{"code": "invalid_request", "message": "Invalid JSON"}})
		return
	}
	if fields["draft"] == true || fields["draft_task_id"] != nil {
		c.JSON(400, gin.H{"error": gin.H{"code": "unsupported_quote", "message": "Quotes currently support standard generation requests"}})
		return
	}
	info, err := relaycommon.GenRelayInfo(c, types.RelayFormatTask, nil, nil)
	if err != nil {
		c.JSON(400, gin.H{"error": gin.H{"code": "invalid_request", "message": "Invalid generation request"}})
		return
	}
	details, taskErr := relay.QuoteSeedanceVideo(c, info)
	if taskErr != nil {
		respondTaskError(c, taskErr)
		return
	}
	quoteID, err := common.GenerateRandomCharsKey(24)
	if err != nil {
		c.JSON(500, gin.H{"error": gin.H{"code": "quote_failed", "message": "Unable to create quote"}})
		return
	}
	c.JSON(http.StatusOK, gin.H{"object": "video.quote", "quote_id": "quote_" + quoteID, "expires_at": time.Now().Add(5 * time.Minute).Unix(), "binding": false, "funds_reserved": false, "billing": details})
}

func GetSeedanceBillingReceipt(c *gin.Context) {
	receipt, err := model.GetSeedanceBillingReceipt(c.GetInt("id"), c.Param("task_id"))
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(404, gin.H{"error": gin.H{"code": "receipt_not_found", "message": "Receipt not found; receipts are available for tasks submitted after this feature was enabled"}})
		} else {
			c.JSON(500, gin.H{"error": gin.H{"code": "receipt_unavailable", "message": "Unable to retrieve receipt"}})
		}
		return
	}
	var details model.SeedanceBillingDetails
	if err := common.Unmarshal(receipt.Details, &details); err != nil {
		c.JSON(500, gin.H{"error": gin.H{"code": "receipt_unavailable", "message": "Unable to retrieve receipt"}})
		return
	}
	c.JSON(200, gin.H{"object": "video.billing_receipt", "billing": details})
}
