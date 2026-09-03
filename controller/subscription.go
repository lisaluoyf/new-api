package controller

import (
	"math"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// ---- Shared types ----

type SubscriptionPlanDTO struct {
	Plan model.SubscriptionPlan `json:"plan"`
}

type BillingPreferenceRequest struct {
	BillingPreference string `json:"billing_preference"`
}

// ---- User APIs ----

func GetSubscriptionPlans(c *gin.Context) {
	var plans []model.SubscriptionPlan
	if err := model.DB.Where("enabled = ?", true).Order("sort_order desc, id desc").Find(&plans).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	result := make([]SubscriptionPlanDTO, 0, len(plans))
	for _, p := range plans {
		// Trial plans are issued exclusively by the signup sharing flow. They
		// must never appear as a purchasable subscription option.
		if model.IsGPTPromotionalSubscriptionPlan(&p) || model.IsCodingPlan(&p) {
			continue
		}
		result = append(result, SubscriptionPlanDTO{
			Plan: p,
		})
	}
	common.ApiSuccess(c, result)
}

func GetSubscriptionSelf(c *gin.Context) {
	userId := c.GetInt("id")
	settingMap, _ := model.GetUserSetting(userId, false)
	pref := common.NormalizeBillingPreference(settingMap.BillingPreference)

	// Get all subscriptions (including expired). Referral GPT rewards are
	// displayed on the affiliate page, not in the wallet subscription card.
	allSubscriptions, err := model.GetAllUserSubscriptions(userId)
	if err != nil {
		allSubscriptions = []model.SubscriptionSummary{}
	}

	// Get active subscriptions for backward compatibility
	activeSubscriptions, err := model.GetAllActiveUserSubscriptions(userId)
	if err != nil {
		activeSubscriptions = []model.SubscriptionSummary{}
	}
	planByID := make(map[int]model.SubscriptionPlan)
	hiddenPlanIDs := make(map[int]struct{})
	for _, summary := range allSubscriptions {
		if summary.Subscription == nil || summary.Subscription.PlanId <= 0 {
			continue
		}
		plan, err := model.GetSubscriptionPlanById(summary.Subscription.PlanId)
		if err == nil && plan != nil {
			if model.IsGPTReferralRewardSubscriptionPlan(plan) {
				hiddenPlanIDs[plan.Id] = struct{}{}
				continue
			}
			// Historical Trial plans can predate plan_type. Preserve their stable
			// classification in the self response even when the campaign is disabled.
			if model.IsGPTTrialSubscriptionPlan(plan) {
				plan.PlanType = model.SubscriptionPlanTypeGPTTrial
			}
			planByID[plan.Id] = *plan
		}
	}
	filterVisible := func(subscriptions []model.SubscriptionSummary) []model.SubscriptionSummary {
		visible := make([]model.SubscriptionSummary, 0, len(subscriptions))
		for _, summary := range subscriptions {
			if summary.Subscription == nil {
				continue
			}
			if _, hidden := hiddenPlanIDs[summary.Subscription.PlanId]; hidden {
				continue
			}
			visible = append(visible, summary)
		}
		return visible
	}
	allSubscriptions = filterVisible(allSubscriptions)
	activeSubscriptions = filterVisible(activeSubscriptions)
	if trialPlan, err := model.GetActiveGPTTrialPlan(); err == nil && trialPlan != nil {
		planByID[trialPlan.Id] = *trialPlan
	}
	plans := make([]SubscriptionPlanDTO, 0, len(planByID))
	for _, plan := range planByID {
		plans = append(plans, SubscriptionPlanDTO{Plan: plan})
	}

	common.ApiSuccess(c, gin.H{
		"billing_preference": pref,
		"subscriptions":      activeSubscriptions, // all active subscriptions
		"all_subscriptions":  allSubscriptions,    // all subscriptions including expired
		"plans":              plans,               // owned plans plus the current trial preview
	})
}

func UpdateSubscriptionPreference(c *gin.Context) {
	userId := c.GetInt("id")
	var req BillingPreferenceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorMsg(c, "参数错误")
		return
	}
	pref := common.NormalizeBillingPreference(req.BillingPreference)

	user, err := model.GetUserById(userId, true)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	current := user.GetSetting()
	current.BillingPreference = pref
	user.SetSetting(current)
	if err := user.Update(false); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"billing_preference": pref})
}

// ---- Admin APIs ----

func AdminListSubscriptionPlans(c *gin.Context) {
	var plans []model.SubscriptionPlan
	if err := model.DB.Order("sort_order desc, id desc").Find(&plans).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	result := make([]SubscriptionPlanDTO, 0, len(plans))
	for _, p := range plans {
		if model.IsGPTReferralRewardSubscriptionPlan(&p) {
			continue
		}
		// Preserve the legacy campaign's identity in the admin form until its
		// plan_type has been persisted by the campaign lookup or an edit.
		if model.IsGPTTrialSubscriptionPlan(&p) {
			p.PlanType = model.SubscriptionPlanTypeGPTTrial
		}
		result = append(result, SubscriptionPlanDTO{
			Plan: p,
		})
	}
	common.ApiSuccess(c, result)
}

type AdminUpsertSubscriptionPlanRequest struct {
	Plan model.SubscriptionPlan `json:"plan"`
}

func normalizeCodingPlanForAdmin(plan *model.SubscriptionPlan) error {
	if plan == nil || !model.IsCodingPlan(plan) {
		return nil
	}
	plan.DurationUnit = model.SubscriptionDurationDay
	plan.DurationValue = 30
	plan.CustomSeconds = 0
	plan.QuotaResetPeriod = model.SubscriptionResetNever
	plan.QuotaResetCustomSeconds = 0
	plan.FiveHourAmount = 0
	plan.SevenDayAmount = 0
	plan.CreemProductId = ""
	plan.TotalAmount = int64(math.Round(plan.CodingOfficialAmountUSD * common.QuotaPerUnit))
	normalized, err := model.CodingModelMultipliersJSON(plan.CodingModelMultipliers)
	if err != nil {
		return err
	}
	plan.CodingModelMultipliers = normalized
	return model.ValidateCodingPlan(plan)
}

func AdminCreateSubscriptionPlan(c *gin.Context) {
	var req AdminUpsertSubscriptionPlanRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorMsg(c, "参数错误")
		return
	}
	req.Plan.Id = 0
	if strings.TrimSpace(req.Plan.Title) == "" {
		common.ApiErrorMsg(c, "套餐标题不能为空")
		return
	}
	if req.Plan.PriceAmount < 0 {
		common.ApiErrorMsg(c, "价格不能为负数")
		return
	}
	if req.Plan.PriceAmount > 9999 {
		common.ApiErrorMsg(c, "价格不能超过9999")
		return
	}
	if req.Plan.Currency == "" {
		req.Plan.Currency = "USD"
	}
	req.Plan.Currency = "USD"
	if req.Plan.DurationUnit == "" {
		req.Plan.DurationUnit = model.SubscriptionDurationMonth
	}
	if req.Plan.DurationValue <= 0 && req.Plan.DurationUnit != model.SubscriptionDurationCustom {
		req.Plan.DurationValue = 1
	}
	if req.Plan.MaxPurchasePerUser < 0 {
		common.ApiErrorMsg(c, "购买上限不能为负数")
		return
	}
	if req.Plan.TotalAmount < 0 {
		common.ApiErrorMsg(c, "总额度不能为负数")
		return
	}
	if req.Plan.TierLevel < 0 || req.Plan.FiveHourAmount < 0 || req.Plan.SevenDayAmount < 0 {
		common.ApiErrorMsg(c, "GPT套餐等级和滚动额度不能为负数")
		return
	}
	if !model.IsSupportedSubscriptionPlanType(req.Plan.PlanType) {
		common.ApiErrorMsg(c, "无效的套餐类型")
		return
	}
	req.Plan.PlanType = model.NormalizeSubscriptionPlanType(req.Plan.PlanType)
	if err := normalizeCodingPlanForAdmin(&req.Plan); err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	req.Plan.UpgradeGroup = strings.TrimSpace(req.Plan.UpgradeGroup)
	if req.Plan.UpgradeGroup != "" {
		if _, ok := ratio_setting.GetGroupRatioCopy()[req.Plan.UpgradeGroup]; !ok {
			common.ApiErrorMsg(c, "升级分组不存在")
			return
		}
	}
	req.Plan.QuotaResetPeriod = model.NormalizeResetPeriod(req.Plan.QuotaResetPeriod)
	if req.Plan.QuotaResetPeriod == model.SubscriptionResetCustom && req.Plan.QuotaResetCustomSeconds <= 0 {
		common.ApiErrorMsg(c, "自定义重置周期需大于0秒")
		return
	}
	err := model.DB.Create(&req.Plan).Error
	if err != nil {
		common.ApiError(c, err)
		return
	}
	model.InvalidateSubscriptionPlanCache(req.Plan.Id)
	common.ApiSuccess(c, req.Plan)
}

func AdminUpdateSubscriptionPlan(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	if id <= 0 {
		common.ApiErrorMsg(c, "无效的ID")
		return
	}
	var req AdminUpsertSubscriptionPlanRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorMsg(c, "参数错误")
		return
	}
	if strings.TrimSpace(req.Plan.Title) == "" {
		common.ApiErrorMsg(c, "套餐标题不能为空")
		return
	}
	if req.Plan.PriceAmount < 0 {
		common.ApiErrorMsg(c, "价格不能为负数")
		return
	}
	if req.Plan.PriceAmount > 9999 {
		common.ApiErrorMsg(c, "价格不能超过9999")
		return
	}
	req.Plan.Id = id
	if req.Plan.Currency == "" {
		req.Plan.Currency = "USD"
	}
	req.Plan.Currency = "USD"
	if req.Plan.DurationUnit == "" {
		req.Plan.DurationUnit = model.SubscriptionDurationMonth
	}
	if req.Plan.DurationValue <= 0 && req.Plan.DurationUnit != model.SubscriptionDurationCustom {
		req.Plan.DurationValue = 1
	}
	if req.Plan.MaxPurchasePerUser < 0 {
		common.ApiErrorMsg(c, "购买上限不能为负数")
		return
	}
	if req.Plan.TotalAmount < 0 {
		common.ApiErrorMsg(c, "总额度不能为负数")
		return
	}
	if req.Plan.TierLevel < 0 || req.Plan.FiveHourAmount < 0 || req.Plan.SevenDayAmount < 0 {
		common.ApiErrorMsg(c, "GPT套餐等级和滚动额度不能为负数")
		return
	}
	if !model.IsSupportedSubscriptionPlanType(req.Plan.PlanType) {
		common.ApiErrorMsg(c, "无效的套餐类型")
		return
	}
	req.Plan.PlanType = model.NormalizeSubscriptionPlanType(req.Plan.PlanType)
	if err := normalizeCodingPlanForAdmin(&req.Plan); err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	req.Plan.UpgradeGroup = strings.TrimSpace(req.Plan.UpgradeGroup)
	if req.Plan.UpgradeGroup != "" {
		if _, ok := ratio_setting.GetGroupRatioCopy()[req.Plan.UpgradeGroup]; !ok {
			common.ApiErrorMsg(c, "升级分组不存在")
			return
		}
	}
	req.Plan.QuotaResetPeriod = model.NormalizeResetPeriod(req.Plan.QuotaResetPeriod)
	if req.Plan.QuotaResetPeriod == model.SubscriptionResetCustom && req.Plan.QuotaResetCustomSeconds <= 0 {
		common.ApiErrorMsg(c, "自定义重置周期需大于0秒")
		return
	}

	err := model.DB.Transaction(func(tx *gorm.DB) error {
		// update plan (allow zero values updates with map)
		updateMap := map[string]interface{}{
			"title":                      req.Plan.Title,
			"subtitle":                   req.Plan.Subtitle,
			"plan_type":                  req.Plan.PlanType,
			"price_amount":               req.Plan.PriceAmount,
			"currency":                   req.Plan.Currency,
			"duration_unit":              req.Plan.DurationUnit,
			"duration_value":             req.Plan.DurationValue,
			"custom_seconds":             req.Plan.CustomSeconds,
			"enabled":                    req.Plan.Enabled,
			"sort_order":                 req.Plan.SortOrder,
			"stripe_price_id":            req.Plan.StripePriceId,
			"creem_product_id":           req.Plan.CreemProductId,
			"max_purchase_per_user":      req.Plan.MaxPurchasePerUser,
			"total_amount":               req.Plan.TotalAmount,
			"upgrade_group":              req.Plan.UpgradeGroup,
			"quota_reset_period":         req.Plan.QuotaResetPeriod,
			"quota_reset_custom_seconds": req.Plan.QuotaResetCustomSeconds,
			"tier_level":                 req.Plan.TierLevel,
			"five_hour_amount":           req.Plan.FiveHourAmount,
			"seven_day_amount":           req.Plan.SevenDayAmount,
			"model_allowlist":            strings.TrimSpace(req.Plan.ModelAllowlist),
			"recommended":                req.Plan.Recommended,
			"card_description":           req.Plan.CardDescription,
			"coding_official_amount_usd": req.Plan.CodingOfficialAmountUSD,
			"coding_model_multipliers":   req.Plan.CodingModelMultipliers,
			"updated_at":                 common.GetTimestamp(),
		}
		if err := tx.Model(&model.SubscriptionPlan{}).Where("id = ?", id).Updates(updateMap).Error; err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		common.ApiError(c, err)
		return
	}
	model.InvalidateSubscriptionPlanCache(id)
	common.ApiSuccess(c, nil)
}

func AdminDeleteSubscriptionPlan(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	if id <= 0 {
		common.ApiErrorMsg(c, "无效的ID")
		return
	}
	plan, err := model.GetSubscriptionPlanById(id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if !model.IsGPTPaidSubscriptionPlan(plan) && !model.IsCodingPlan(plan) {
		common.ApiErrorMsg(c, "仅付费订阅套餐支持删除")
		return
	}
	var references int64
	if err := model.DB.Model(&model.UserSubscription{}).Where("plan_id = ?", id).Count(&references).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	var orderReferences int64
	if err := model.DB.Model(&model.SubscriptionOrder{}).Where("plan_id = ?", id).Count(&orderReferences).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	references += orderReferences
	if references > 0 {
		common.ApiErrorMsg(c, "套餐已有订阅记录，只能下架，不能删除")
		return
	}
	if err := model.DB.Delete(&model.SubscriptionPlan{}, id).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	model.InvalidateSubscriptionPlanCache(id)
	common.ApiSuccess(c, nil)
}

type AdminUpdateSubscriptionPlanStatusRequest struct {
	Enabled *bool `json:"enabled"`
	SoldOut *bool `json:"sold_out"`
}

func AdminUpdateSubscriptionPlanStatus(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	if id <= 0 {
		common.ApiErrorMsg(c, "无效的ID")
		return
	}
	var req AdminUpdateSubscriptionPlanStatusRequest
	if err := c.ShouldBindJSON(&req); err != nil || (req.Enabled == nil && req.SoldOut == nil) {
		common.ApiErrorMsg(c, "参数错误")
		return
	}
	updates := map[string]interface{}{"updated_at": common.GetTimestamp()}
	if req.Enabled != nil {
		updates["enabled"] = *req.Enabled
	}
	if req.SoldOut != nil {
		var plan model.SubscriptionPlan
		if err := model.DB.Where("id = ?", id).First(&plan).Error; err != nil {
			common.ApiError(c, err)
			return
		}
		if !model.IsCodingPlan(&plan) {
			common.ApiErrorMsg(c, "仅 Coding Plan 支持售罄状态")
			return
		}
		updates["sold_out"] = *req.SoldOut
	}
	if err := model.DB.Model(&model.SubscriptionPlan{}).Where("id = ?", id).Updates(updates).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	model.InvalidateSubscriptionPlanCache(id)
	common.ApiSuccess(c, nil)
}

type AdminBindSubscriptionRequest struct {
	UserId int `json:"user_id"`
	PlanId int `json:"plan_id"`
}

func AdminBindSubscription(c *gin.Context) {
	var req AdminBindSubscriptionRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.UserId <= 0 || req.PlanId <= 0 {
		common.ApiErrorMsg(c, "参数错误")
		return
	}
	msg, err := model.AdminBindSubscription(req.UserId, req.PlanId, "")
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if msg != "" {
		common.ApiSuccess(c, gin.H{"message": msg})
		return
	}
	common.ApiSuccess(c, nil)
}

// ---- Admin: user subscription management ----

func AdminListUserSubscriptions(c *gin.Context) {
	userId, _ := strconv.Atoi(c.Param("id"))
	if userId <= 0 {
		common.ApiErrorMsg(c, "无效的用户ID")
		return
	}
	subs, err := model.GetAllUserSubscriptions(userId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, subs)
}

type AdminCreateUserSubscriptionRequest struct {
	PlanId int `json:"plan_id"`
}

// AdminCreateUserSubscription creates a new user subscription from a plan (no payment).
func AdminCreateUserSubscription(c *gin.Context) {
	userId, _ := strconv.Atoi(c.Param("id"))
	if userId <= 0 {
		common.ApiErrorMsg(c, "无效的用户ID")
		return
	}
	var req AdminCreateUserSubscriptionRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.PlanId <= 0 {
		common.ApiErrorMsg(c, "参数错误")
		return
	}
	msg, err := model.AdminBindSubscription(userId, req.PlanId, "")
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if msg != "" {
		common.ApiSuccess(c, gin.H{"message": msg})
		return
	}
	common.ApiSuccess(c, nil)
}

// AdminInvalidateUserSubscription cancels a user subscription immediately.
func AdminInvalidateUserSubscription(c *gin.Context) {
	subId, _ := strconv.Atoi(c.Param("id"))
	if subId <= 0 {
		common.ApiErrorMsg(c, "无效的订阅ID")
		return
	}
	msg, err := model.AdminInvalidateUserSubscription(subId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if msg != "" {
		common.ApiSuccess(c, gin.H{"message": msg})
		return
	}
	common.ApiSuccess(c, nil)
}

// AdminDeleteUserSubscription hard-deletes a user subscription.
func AdminDeleteUserSubscription(c *gin.Context) {
	subId, _ := strconv.Atoi(c.Param("id"))
	if subId <= 0 {
		common.ApiErrorMsg(c, "无效的订阅ID")
		return
	}
	msg, err := model.AdminDeleteUserSubscription(subId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if msg != "" {
		common.ApiSuccess(c, gin.H{"message": msg})
		return
	}
	common.ApiSuccess(c, nil)
}
