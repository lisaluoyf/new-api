package model

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	GPTSubscriptionPublicEnabledOption   = "GPTSubscriptionPublicEnabled"
	GPTSubscriptionWhitelistOption       = "GPTSubscriptionWhitelistEmails"
	DefaultGPTSubscriptionModelAllowlist = "gpt-5.4,gpt-5.4-mini,gpt-5.5,gpt-5.6-luna,gpt-5.6-sol,gpt-5.6-terra"
)

var (
	ErrGPTSubscriptionPlanUnavailable = errors.New("GPT subscription plan is unavailable")
	ErrGPTSubscriptionPlanNotFree     = errors.New("GPT subscription plan is not free")
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

func getOptionString(key, fallback string) string {
	common.OptionMapRWMutex.RLock()
	defer common.OptionMapRWMutex.RUnlock()
	if value, ok := common.OptionMap[key]; ok {
		return value
	}
	return fallback
}

func GPTSubscriptionPublicEnabled() bool {
	enabled, _ := strconv.ParseBool(getOptionString(GPTSubscriptionPublicEnabledOption, "false"))
	return enabled
}

func GPTSubscriptionWhitelist() []string {
	raw := getOptionString(GPTSubscriptionWhitelistOption, "lisa.luoyf@gmail.com")
	seen := map[string]struct{}{}
	result := make([]string, 0)
	for _, value := range strings.Split(raw, ",") {
		email := strings.ToLower(strings.TrimSpace(value))
		if email == "" {
			continue
		}
		if _, ok := seen[email]; ok {
			continue
		}
		seen[email] = struct{}{}
		result = append(result, email)
	}
	sort.Strings(result)
	return result
}

func UpdateGPTSubscriptionAccessConfig(publicEnabled bool, whitelist []string) error {
	clean := make([]string, 0, len(whitelist))
	seen := map[string]struct{}{}
	for _, value := range whitelist {
		email := strings.ToLower(strings.TrimSpace(value))
		if email == "" {
			continue
		}
		if _, ok := seen[email]; ok {
			continue
		}
		seen[email] = struct{}{}
		clean = append(clean, email)
	}
	sort.Strings(clean)
	if err := UpdateOption(GPTSubscriptionPublicEnabledOption, strconv.FormatBool(publicEnabled)); err != nil {
		return err
	}
	return UpdateOption(GPTSubscriptionWhitelistOption, strings.Join(clean, ","))
}

func resolveAPIMasterEmailByUsername(username string) string {
	username = strings.TrimSpace(username)
	if APIMASTER_PG_DB == nil || username == "" {
		return ""
	}
	var email string
	_ = APIMASTER_PG_DB.Raw(`
		SELECT LOWER(COALESCE(email, ''))
		FROM users
		WHERE LEFT(REPLACE(id::text, '-', ''), 20) = ?
		LIMIT 1
	`, username).Scan(&email).Error
	return strings.ToLower(strings.TrimSpace(email))
}

func GetGPTSubscriptionAccess(userId int) (GPTSubscriptionAccess, error) {
	access := GPTSubscriptionAccess{
		PublicEnabled: GPTSubscriptionPublicEnabled(),
		Whitelist:     GPTSubscriptionWhitelist(),
	}
	if userId <= 0 {
		return access, nil
	}
	var user User
	if err := DB.Select("id", "username", "email").Where("id = ?", userId).First(&user).Error; err != nil {
		return access, err
	}
	emails := []string{strings.ToLower(strings.TrimSpace(user.Email)), resolveAPIMasterEmailByUsername(user.Username)}
	whitelisted := false
	for _, email := range emails {
		for _, allowed := range access.Whitelist {
			if email != "" && email == allowed {
				whitelisted = true
				break
			}
		}
	}
	access.CanPurchase = access.PublicEnabled || whitelisted
	access.Allowed = access.CanPurchase
	if !access.Allowed {
		active, err := HasActiveUserSubscriptionByPlanMatcher(userId, IsGPTPaidSubscriptionPlan)
		if err != nil {
			return access, err
		}
		access.Allowed = active
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
	err := DB.Where("plan_type = ? AND enabled = ?", SubscriptionPlanTypeGPTSubscription, true).
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
		{"Pro", "Starter", 5, 5, 33, 1, 500, false},
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

// ActivateFreeGPTSubscription activates an enabled zero-price GPT plan without
// entering the payment or top-up ledger. Repeated activation of the same active
// plan is idempotent and deliberately does not extend its expiry.
func ActivateFreeGPTSubscription(userId int, planId int) (*UserSubscription, bool, error) {
	if userId <= 0 || planId <= 0 {
		return nil, false, errors.New("invalid user or plan")
	}

	var subscription *UserSubscription
	activated := false
	err := DB.Transaction(func(tx *gorm.DB) error {
		// The user lock serializes activations across different free plans for the
		// same account. SQLite ignores FOR UPDATE but serializes transaction writes.
		var user User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Select("id").Where("id = ?", userId).First(&user).Error; err != nil {
			return err
		}

		var plan SubscriptionPlan
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ?", planId).First(&plan).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrGPTSubscriptionPlanUnavailable
			}
			return err
		}
		if !plan.Enabled || !IsGPTPaidSubscriptionPlan(&plan) {
			return ErrGPTSubscriptionPlanUnavailable
		}
		if plan.PriceAmount != 0 {
			return ErrGPTSubscriptionPlanNotFree
		}

		orderType := "purchase"
		previousID := 0
		previousEndTime := int64(0)
		previousCycleID := 0
		current, currentPlan, findErr := activeGPTPaidSubscriptionTx(tx, userId)
		if findErr == nil {
			if current.PlanId == plan.Id {
				subscription = current
				return nil
			}
			currentLevel := current.TierLevelSnapshot
			if currentLevel == 0 {
				currentLevel = currentPlan.TierLevel
			}
			if plan.TierLevel <= currentLevel {
				return errors.New("active GPT subscription can only renew or upgrade")
			}
			orderType = "upgrade"
			previousID = current.Id
			previousEndTime = current.EndTime
			previousCycleID = current.CurrentCycleId
		} else if !errors.Is(findErr, gorm.ErrRecordNotFound) {
			return findErr
		}

		now := GetDBTimestampTx(tx)
		order := SubscriptionOrder{
			UserId:                 userId,
			PlanId:                 plan.Id,
			Money:                  0,
			ListPrice:              0,
			CreditAmount:           0,
			OrderType:              orderType,
			PreviousSubscriptionId: previousID,
			PreviousEndTime:        previousEndTime,
			PreviousCycleId:        previousCycleID,
			TradeNo:                fmt.Sprintf("free_gpt_%d_%d_%s", userId, time.Now().UnixNano(), common.GetRandomString(8)),
			PaymentMethod:          PaymentMethodFree,
			PaymentProvider:        PaymentProviderFree,
			Status:                 common.TopUpStatusSuccess,
			CreateTime:             now,
			CompleteTime:           now,
		}
		if err := tx.Create(&order).Error; err != nil {
			return err
		}
		created, err := completeGPTSubscriptionOrderWithSourceTx(tx, &order, &plan, "free")
		if err != nil {
			return err
		}
		subscription = created
		activated = true
		return nil
	})
	return subscription, activated, err
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
