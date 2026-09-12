package controller

import (
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

type InvitePromoEventRequest struct {
	Event string `json:"event"`
}

func RecordInvitePromoEvent(c *gin.Context) {
	userId := c.GetInt("id")
	var req InvitePromoEventRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	event := strings.TrimSpace(req.Event)
	if !model.IsValidInvitePromoEvent(event) {
		common.ApiErrorI18n(c, i18n.MsgInvitePromoInvalidEvent)
		return
	}
	if err := model.RecordInvitePromoEvent(userId, event); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, nil)
}
