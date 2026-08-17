package model

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

const (
	GPTSubscriptionPublicEnabledOption   = "GPTSubscriptionPublicEnabled"
	GPTSubscriptionWhitelistOption       = "GPTSubscriptionWhitelistEmails"
	DefaultGPTSubscriptionModelAllowlist = "gpt-5.4,gpt-5.4-mini,gpt-5.5,gpt-5.6-luna,gpt-5.6-sol,gpt-5.6-terra"
)

var (
	ErrGPTSubscriptionPlanUnavailable = errors.New("GPT subscription plan is unavailable")
)

type GPTSubscriptionAccess struct {
	PublicEnabled bool     `json:"public_enabled"`
	Allowed       bool     `json:"allowed"`
	CanPurchase   bool     `json:"can_purchase"`
	Whitelist     []string `json:"whitelist,omitempty"`
}

type GPTSubscriptionState struct {
	Subscription *UserSubscription `json:"subscription,omitempty"`
	Plan         *SubscriptionPlan `json:"plan,omitempty"`
	FiveHourUsed int64             `json:"five_hour_used"`
	SevenDayUsed int64             `json:"seven_day_used"`
}

// GPTSubscriptionRollingLimitInfo describes which strict rolling windows block
// a request and the earliest time at which enough historical usage is expected
// to leave those windows. Timestamps are Unix seconds so clients can format
// them in the user's own timezone.
type GPTSubscriptionRollingLimitInfo struct {
	LimitedWindows      []string `json:"limited_windows"`
	AvailableAt         int64    `json:"available_at,omitempty"`
	RetryAfterSeconds   int64    `json:"retry_after_seconds,omitempty"`
	FiveHourAvailableAt int64    `json:"five_hour_available_at,omitempty"`
	SevenDayAvailableAt int64    `json:"seven_day_available_at,omitempty"`
	FiveHourUsed        int64    `json:"five_hour_used"`
	FiveHourLimit       int64    `json:"five_hour_limit"`
	SevenDayUsed        int64    `json:"seven_day_used"`
	SevenDayLimit       int64    `json:"seven_day_limit"`
	RequestedQuota      int64    `json:"requested_quota"`
}

type GPTSubscriptionRollingLimitError struct {
	Info GPTSubscriptionRollingLimitInfo
}

func (e *GPTSubscriptionRollingLimitError) Error() string {
	if e == nil {
		return "subscription quota insufficient: GPT subscription rolling limit reached"
	}
	return fmt.Sprintf("subscription quota insufficient: GPT subscription rolling limit reached, windows=%s, need=%d",
		strings.Join(e.Info.LimitedWindows, ","), e.Info.RequestedQuota)
}

func GPTSubscriptionPublicEnabled() bool {
	return true
}

func GPTSubscriptionWhitelist() []string {
	return []string{}
}

func UpdateGPTSubscriptionAccessConfig(_ bool, _ []string) error {
	if err := UpdateOption(GPTSubscriptionPublicEnabledOption, "true"); err != nil {
		return err
	}
	return UpdateOption(GPTSubscriptionWhitelistOption, "")
}

func GetGPTSubscriptionAccess(userId int) (GPTSubscriptionAccess, error) {
	access := GPTSubscriptionAccess{
		PublicEnabled: true,
		Allowed:       userId > 0,
		CanPurchase:   userId > 0,
		Whitelist:     []string{},
	}
	return access, nil
}

func IsModelAllowedForGPTSubscription(plan *SubscriptionPlan, modelName string) bool {
	if plan == nil || !IsGPTPaidSubscriptionPlan(plan) {
		return false
	}
	wanted := strings.ToLower(strings.TrimSpace(modelName))
	if wanted == "" {
		return false
	}
	for _, candidate := range strings.Split(plan.ModelAllowlist, ",") {
		if strings.ToLower(strings.TrimSpace(candidate)) == wanted {
			return true
		}
	}
	return false
}

func GetEnabledGPTSubscriptionPlans() ([]SubscriptionPlan, error) {
	var plans []SubscriptionPlan
	err := DB.Where("plan_type = ? AND enabled = ? AND price_amount > ?", SubscriptionPlanTypeGPTSubscription, true, 0).
		Order("sort_order desc, tier_level asc, id asc").Find(&plans).Error
	return plans, err
}

func GetGPTSubscriptionState(userId int) (GPTSubscriptionState, error) {
	state := GPTSubscriptionState{}
	current, plan, err := activeGPTPaidSubscriptionTx(DB, userId)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return state, nil
	}
	if err != nil {
		return state, err
	}
	state.Subscription = current
	state.Plan = plan
	state.FiveHourUsed, state.SevenDayUsed, err = getGPTSubscriptionRollingUsageTx(DB, userId, GetDBTimestamp())
	return state, err
}

func EnrichUsersGPTSubscriptionStatus(users []*User) {
	ids := make([]int, 0, len(users))
	for _, user := range users {
		if user != nil && user.Id > 0 {
			ids = append(ids, user.Id)
		}
	}
	if len(ids) == 0 {
		return
	}
	var subs []UserSubscription
	if err := DB.Where("user_id IN ?", ids).Order("end_time desc, id desc").Find(&subs).Error; err != nil {
		return
	}
	planIDs := make([]int, 0)
	seenPlans := map[int]struct{}{}
	for _, sub := range subs {
		if _, ok := seenPlans[sub.PlanId]; !ok {
			seenPlans[sub.PlanId] = struct{}{}
			planIDs = append(planIDs, sub.PlanId)
		}
	}
	var plans []SubscriptionPlan
	if len(planIDs) > 0 {
		_ = DB.Where("id IN ? AND plan_type = ?", planIDs, SubscriptionPlanTypeGPTSubscription).Find(&plans).Error
	}
	planByID := map[int]SubscriptionPlan{}
	for _, plan := range plans {
		planByID[plan.Id] = plan
	}
	now := GetDBTimestamp()
	chosen := map[int]UserSubscription{}
	for _, sub := range subs {
		if _, ok := planByID[sub.PlanId]; !ok {
			continue
		}
		current, exists := chosen[sub.UserId]
		isActive := sub.Status == "active" && sub.EndTime > now
		currentActive := exists && current.Status == "active" && current.EndTime > now
		if !exists || (isActive && !currentActive) {
			chosen[sub.UserId] = sub
		}
	}
	for _, user := range users {
		if user == nil {
			continue
		}
		sub, ok := chosen[user.Id]
		if !ok {
			continue
		}
		plan := planByID[sub.PlanId]
		title := strings.TrimSpace(sub.PlanTitleSnapshot)
		if title == "" {
			title = plan.Title
		}
		status := sub.Status
		if status == "active" && sub.EndTime <= now {
			status = "expired"
		}
		user.GPTSubscriptionStatus = status
		user.GPTSubscriptionPlanId = sub.PlanId
		user.GPTSubscriptionPlanTitle = title
		user.GPTSubscriptionEndTime = sub.EndTime
	}
}

func subscriptionDurationSeconds(startUnix, endUnix int64) int64 {
	if endUnix <= startUnix {
		return 0
	}
	return endUnix - startUnix
}

func SeedDefaultGPTSubscriptionPlans() error {
	var count int64
	if err := DB.Model(&SubscriptionPlan{}).Where("plan_type = ?", SubscriptionPlanTypeGPTSubscription).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	type seed struct {
		title, subtitle string
		price           float64
		five, seven     float64
		level, order    int
		recommended     bool
	}
	seeds := []seed{
		{"Pro+", "Standard", 10, 10, 66, 2, 400, false},
		{"Max", "Professional", 20, 20, 132, 3, 300, true},
		{"Ultra", "Advanced", 100, 100, 660, 4, 200, false},
		{"Power", "Flagship", 200, 200, 1320, 5, 100, false},
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		for _, item := range seeds {
			plan := SubscriptionPlan{
				Title: item.title, Subtitle: item.subtitle,
				PlanType:    SubscriptionPlanTypeGPTSubscription,
				PriceAmount: item.price, Currency: "USD",
				DurationUnit: SubscriptionDurationDay, DurationValue: 30,
				Enabled: true, SortOrder: item.order,
				TotalAmount: 0, QuotaResetPeriod: SubscriptionResetNever,
				TierLevel:       item.level,
				FiveHourAmount:  int64(math.Round(item.five * common.QuotaPerUnit)),
				SevenDayAmount:  int64(math.Round(item.seven * common.QuotaPerUnit)),
				ModelAllowlist:  DefaultGPTSubscriptionModelAllowlist,
				Recommended:     item.recommended,
				CardDescription: "",
			}
			if err := tx.Create(&plan).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func activeGPTPaidSubscriptionTx(tx *gorm.DB, userId int) (*UserSubscription, *SubscriptionPlan, error) {
	if tx == nil {
		tx = DB
	}
	now := GetDBTimestampTx(tx)
	var subs []UserSubscription
	if err := tx.Where("user_id = ? AND status = ? AND end_time > ?", userId, "active", now).
		Order("end_time desc, id desc").Find(&subs).Error; err != nil {
		return nil, nil, err
	}
	for i := range subs {
		plan, err := getSubscriptionPlanByIdTx(tx, subs[i].PlanId)
		if err != nil {
			return nil, nil, err
		}
		if IsGPTPaidSubscriptionPlan(plan) {
			return &subs[i], plan, nil
		}
	}
	return nil, nil, gorm.ErrRecordNotFound
}

func CalculateGPTSubscriptionQuote(userId int, target *SubscriptionPlan) (orderType string, previousId int, credit, payable float64, err error) {
	if target == nil || !IsGPTPaidSubscriptionPlan(target) {
		return "", 0, 0, 0, errors.New("invalid GPT subscription plan")
	}
	payable = target.PriceAmount
	orderType = "purchase"
	current, currentPlan, findErr := activeGPTPaidSubscriptionTx(DB, userId)
	if errors.Is(findErr, gorm.ErrRecordNotFound) {
		return orderType, 0, 0, payable, nil
	}
	if findErr != nil {
		return "", 0, 0, 0, findErr
	}
	previousId = current.Id
	if current.PlanId == target.Id {
		return "renewal", previousId, 0, payable, nil
	}
	currentLevel := current.TierLevelSnapshot
	if currentLevel == 0 {
		currentLevel = currentPlan.TierLevel
	}
	if target.TierLevel <= currentLevel {
		return "", previousId, 0, 0, errors.New("active GPT subscription can only renew or upgrade")
	}
	now := GetDBTimestamp()
	duration := current.DurationSecondsSnapshot
	if duration <= 0 {
		duration = subscriptionDurationSeconds(current.StartTime, current.EndTime)
	}
	price := current.PriceAmountSnapshot
	if price <= 0 {
		price = currentPlan.PriceAmount
	}
	if duration > 0 && current.EndTime > now {
		credit = price * float64(current.EndTime-now) / float64(duration)
	}
	if credit > target.PriceAmount {
		credit = target.PriceAmount
	}
	payable = math.Max(0, target.PriceAmount-credit)
	return "upgrade", previousId, credit, payable, nil
}

func completeGPTSubscriptionOrderTx(tx *gorm.DB, order *SubscriptionOrder, plan *SubscriptionPlan) (*UserSubscription, error) {
	return completeGPTSubscriptionOrderWithSourceTx(tx, order, plan, "")
}

func completeGPTSubscriptionOrderWithSourceTx(tx *gorm.DB, order *SubscriptionOrder, plan *SubscriptionPlan, source string) (*UserSubscription, error) {
	if tx == nil || order == nil || plan == nil {
		return nil, errors.New("invalid GPT subscription completion")
	}
	now := GetDBTimestampTx(tx)
	switch order.OrderType {
	case "renewal":
		var current UserSubscription
		if err := tx.Set("gorm:query_option", "FOR UPDATE").Where("id = ? AND user_id = ?", order.PreviousSubscriptionId, order.UserId).First(&current).Error; err != nil {
			return nil, err
		}
		base := now
		if current.EndTime > base {
			base = current.EndTime
		}
		newEnd, err := calcPlanEndTime(time.Unix(base, 0), plan)
		if err != nil {
			return nil, err
		}
		current.EndTime = newEnd
		current.Status = "active"
		current.PlanTitleSnapshot = plan.Title
		current.PlanSubtitleSnapshot = plan.Subtitle
		current.CardDescriptionSnapshot = plan.CardDescription
		current.PriceAmountSnapshot = plan.PriceAmount
		current.DurationSecondsSnapshot = newEnd - base
		current.TierLevelSnapshot = plan.TierLevel
		current.FiveHourAmount = plan.FiveHourAmount
		current.SevenDayAmount = plan.SevenDayAmount
		current.ModelAllowlistSnapshot = plan.ModelAllowlist
		current.CurrentCycleId = order.Id
		if err := tx.Save(&current).Error; err != nil {
			return nil, err
		}
		return &current, nil
	case "upgrade":
		var current UserSubscription
		if err := tx.Set("gorm:query_option", "FOR UPDATE").Where("id = ? AND user_id = ?", order.PreviousSubscriptionId, order.UserId).First(&current).Error; err != nil {
			return nil, err
		}
		if current.Status != "active" || current.EndTime <= now {
			return nil, errors.New("previous GPT subscription is no longer active")
		}
		if err := tx.Model(&current).Updates(map[string]any{"status": "cancelled", "end_time": now, "updated_at": now}).Error; err != nil {
			return nil, err
		}
		if source == "" {
			source = "upgrade"
		}
		created, err := CreateUserSubscriptionFromPlanTx(tx, order.UserId, plan, source)
		if err == nil {
			created.CurrentCycleId = order.Id
			err = tx.Save(created).Error
		}
		return created, err
	default:
		if _, _, err := activeGPTPaidSubscriptionTx(tx, order.UserId); err == nil {
			return nil, errors.New("user already has an active GPT subscription")
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
		if source == "" {
			source = "order"
		}
		created, err := CreateUserSubscriptionFromPlanTx(tx, order.UserId, plan, source)
		if err == nil {
			created.CurrentCycleId = order.Id
			err = tx.Save(created).Error
		}
		return created, err
	}
}

func getGPTSubscriptionRollingUsageTx(tx *gorm.DB, userId int, now int64) (fiveHour, sevenDay int64, err error) {
	type sums struct {
		Five  int64
		Seven int64
	}
	var result sums
	fiveStart := now - int64((5 * time.Hour).Seconds())
	sevenStart := now - int64((7 * 24 * time.Hour).Seconds())
	err = tx.Model(&SubscriptionPreConsumeRecord{}).
		Select("COALESCE(SUM(CASE WHEN created_at >= ? THEN pre_consumed ELSE 0 END), 0) AS five, COALESCE(SUM(CASE WHEN created_at >= ? THEN pre_consumed ELSE 0 END), 0) AS seven", fiveStart, sevenStart).
		Where("user_id = ? AND plan_type = ? AND status = ?", userId, SubscriptionPlanTypeGPTSubscription, "consumed").
		Scan(&result).Error
	return result.Five, result.Seven, err
}

type gptSubscriptionRollingUsageRecord struct {
	PreConsumed int64
	CreatedAt   int64
}

// getGPTSubscriptionRollingLimitInfoTx calculates both the current usage and
// the first future timestamp at which a request of amount can fit. The +1
// second matches the existing inclusive `created_at >= now-window` query: a
// record is still counted at its exact expiry second and leaves on the next
// second.
func getGPTSubscriptionRollingLimitInfoTx(tx *gorm.DB, userId int, now, amount, fiveLimit, sevenLimit int64) (GPTSubscriptionRollingLimitInfo, error) {
	info := GPTSubscriptionRollingLimitInfo{
		FiveHourLimit:  fiveLimit,
		SevenDayLimit:  sevenLimit,
		RequestedQuota: amount,
	}
	if tx == nil {
		tx = DB
	}
	fiveWindow := int64((5 * time.Hour).Seconds())
	sevenWindow := int64((7 * 24 * time.Hour).Seconds())
	var records []gptSubscriptionRollingUsageRecord
	if err := tx.Model(&SubscriptionPreConsumeRecord{}).
		Select("pre_consumed, created_at").
		Where("user_id = ? AND plan_type = ? AND status = ? AND created_at >= ?", userId, SubscriptionPlanTypeGPTSubscription, "consumed", now-sevenWindow).
		Order("created_at asc, id asc").
		Find(&records).Error; err != nil {
		return info, err
	}

	var fiveUsed, sevenUsed int64
	for _, record := range records {
		sevenUsed += record.PreConsumed
		if record.CreatedAt >= now-fiveWindow {
			fiveUsed += record.PreConsumed
		}
	}
	info.FiveHourUsed = fiveUsed
	info.SevenDayUsed = sevenUsed

	calculateAvailableAt := func(window, used, limit int64) int64 {
		if limit <= 0 || used+amount <= limit {
			return 0
		}
		// A request larger than the complete window can never fit by waiting.
		if amount > limit {
			return 0
		}
		remaining := used
		for i := 0; i < len(records); {
			createdAt := records[i].CreatedAt
			expiresAt := createdAt + window + 1
			var expired int64
			for i < len(records) && records[i].CreatedAt == createdAt {
				if window == sevenWindow || records[i].CreatedAt >= now-window {
					expired += records[i].PreConsumed
				}
				i++
			}
			remaining -= expired
			if remaining+amount <= limit {
				return expiresAt
			}
		}
		return 0
	}

	if fiveLimit > 0 && fiveUsed+amount > fiveLimit {
		info.FiveHourAvailableAt = calculateAvailableAt(fiveWindow, fiveUsed, fiveLimit)
		info.LimitedWindows = append(info.LimitedWindows, "5h")
	}
	if sevenLimit > 0 && sevenUsed+amount > sevenLimit {
		info.SevenDayAvailableAt = calculateAvailableAt(sevenWindow, sevenUsed, sevenLimit)
		info.LimitedWindows = append(info.LimitedWindows, "7d")
	}
	if len(info.LimitedWindows) > 0 {
		switch len(info.LimitedWindows) {
		case 1:
			if info.LimitedWindows[0] == "5h" {
				info.AvailableAt = info.FiveHourAvailableAt
			} else {
				info.AvailableAt = info.SevenDayAvailableAt
			}
		default:
			// Both windows must have room. A zero value means one window can
			// never fit this request even after all current usage expires.
			if info.FiveHourAvailableAt > 0 && info.SevenDayAvailableAt > 0 {
				info.AvailableAt = max(info.FiveHourAvailableAt, info.SevenDayAvailableAt)
			}
		}
		if info.AvailableAt > now {
			info.RetryAfterSeconds = info.AvailableAt - now
		}
	}
	return info, nil
}
