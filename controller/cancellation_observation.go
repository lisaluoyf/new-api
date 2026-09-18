package controller

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

// Read-only, root-only diagnostic endpoints. There is deliberately no apply,
// debit, refund or evidence-import endpoint in the observation release.
func ListCancellationObservations(c *gin.Context) {
	values := map[string]int{}
	for _, key := range []string{"user_id", "before_id", "limit"} {
		if raw := c.Query(key); raw != "" {
			v, err := strconv.Atoi(raw)
			if err != nil || v < 0 {
				common.ApiError(c, fmt.Errorf("invalid %s", key))
				return
			}
			values[key] = v
		}
	}
	rows, err := model.ListCancellationObservations(strings.TrimSpace(c.Query("model")), values["user_id"], values["before_id"], values["limit"])
	if err != nil {
		common.ApiError(c, err)
		return
	}
	next := 0
	if len(rows) > 0 {
		next = rows[len(rows)-1].Id
	}
	common.ApiSuccess(c, gin.H{"mode": "observe_only", "automatic_charge_allowed": false, "items": rows, "next_before_id": next})
}

func GetCancellationObservation(c *gin.Context) {
	item, err := model.GetCancellationObservation(strings.TrimSpace(c.Param("request_id")))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"mode": "observe_only", "automatic_charge_allowed": false, "item": item})
}
