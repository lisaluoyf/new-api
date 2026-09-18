package controller

import (
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

func trialBlockedEmailDomainFromUser(c *gin.Context) (string, bool) {
	user, ok := trialAdminTargetUser(c)
	if !ok {
		return "", false
	}
	parts := strings.Split(strings.TrimSpace(user.Email), "@")
	if len(parts) != 2 || strings.TrimSpace(parts[1]) == "" {
		common.ApiErrorI18n(c, i18n.MsgEmailDomainUnavailable)
		return "", false
	}
	return parts[1], true
}

func trialAdminTargetUser(c *gin.Context) (*model.User, bool) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiError(c, err)
		return nil, false
	}
	user, err := model.GetUserById(id, false)
	if err != nil {
		common.ApiError(c, err)
		return nil, false
	}
	myRole := c.GetInt("role")
	if myRole <= user.Role && myRole != common.RoleRootUser {
		common.ApiErrorI18n(c, i18n.MsgUserNoPermissionSameLevel)
		return nil, false
	}
	return user, true
}

func AdminAddTrialBlockedEmailDomain(c *gin.Context) {
	domain, ok := trialBlockedEmailDomainFromUser(c)
	if !ok {
		return
	}
	normalized, err := model.AddTrialBlockedEmailDomain(domain)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"domain": normalized})
}

func AdminRemoveTrialBlockedEmailDomain(c *gin.Context) {
	domain, ok := trialBlockedEmailDomainFromUser(c)
	if !ok {
		return
	}
	normalized, err := model.RemoveTrialBlockedEmailDomain(domain)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"domain": normalized})
}

func AdminAddTrialRiskAllowlist(c *gin.Context) {
	user, ok := trialAdminTargetUser(c)
	if !ok {
		return
	}
	adminId := c.GetInt("id")
	adminUsername := c.GetString("username")
	allowlisted, err := model.SetTrialRiskAllowlist(user, true, adminId, adminUsername)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if err := model.ClearTrialRiskBlockedRemark(user); err != nil {
		_, _ = model.SetTrialRiskAllowlist(user, false, adminId, adminUsername)
		common.ApiError(c, err)
		return
	}
	model.RecordLogWithAdminInfo(user.Id, model.LogTypeManage, "管理员已开启体验卡风控白名单", map[string]interface{}{
		"admin_id":       adminId,
		"admin_username": adminUsername,
	})
	common.ApiSuccess(c, gin.H{"trial_risk_allowlisted": allowlisted})
}

func AdminRemoveTrialRiskAllowlist(c *gin.Context) {
	user, ok := trialAdminTargetUser(c)
	if !ok {
		return
	}
	adminId := c.GetInt("id")
	adminUsername := c.GetString("username")
	allowlisted, err := model.SetTrialRiskAllowlist(user, false, adminId, adminUsername)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	model.RecordLogWithAdminInfo(user.Id, model.LogTypeManage, "管理员已取消体验卡风控白名单", map[string]interface{}{
		"admin_id":       adminId,
		"admin_username": adminUsername,
	})
	common.ApiSuccess(c, gin.H{"trial_risk_allowlisted": allowlisted})
}
