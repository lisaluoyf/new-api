package model

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const referralGPTRewardPermanentEndTime int64 = 253402300799 // 9999-12-31 23:59:59 UTC

var referralGPTRewardPlanMu sync.Mutex

type ReferralGPTRewardLog struct {
	Id                    int    `json:"id" gorm:"primaryKey;autoIncrement"`
	TradeNo               string `json:"trade_no" gorm:"type:varchar(255);not null;uniqueIndex"`
	InviterId             int    `json:"inviter_id" gorm:"not null;index"`
	InviteeId             int    `json:"invitee_id" gorm:"not null;uniqueIndex"`
	TopupQuota            int64  `json:"topup_quota" gorm:"type:bigint;not null"`
	InviterRewardQuota    int64  `json:"inviter_reward_quota" gorm:"type:bigint;not null"`
	InviteeRewardQuota    int64  `json:"invitee_reward_quota" gorm:"type:bigint;not null"`
	InviterSubscriptionId int    `json:"inviter_subscription_id" gorm:"not null"`
	InviteeSubscriptionId int    `json:"invitee_subscription_id" gorm:"not null"`
	PaymentMethod         string `json:"payment_method" gorm:"type:varchar(50);default:''"`
	GrantSource           string `json:"grant_source" gorm:"type:varchar(32);default:'realtime'"`
	GrantedAt             int64  `json:"granted_at" gorm:"not null;index"`
	FeishuNotifiedAt      int64  `json:"feishu_notified_at" gorm:"not null;default:0;index"`
	FeishuNotifyLockedAt  int64  `json:"-" gorm:"not null;default:0"`
	FeishuNotifyAttempts  int    `json:"-" gorm:"not null;default:0"`
}

func (ReferralGPTRewardLog) TableName() string {
	return "referral_gpt_reward_logs"
}

type ReferralGPTRewardSummary struct {
	Enabled           bool    `json:"enabled"`
	MinTopupUSD       float64 `json:"min_topup_usd"`
	RewardUSD         float64 `json:"reward_usd"`
	RemainingQuota    int64   `json:"remaining_quota"`
	CumulativeQuota   int64   `json:"cumulative_quota"`
	QualifiedInvitees int64   `json:"qualified_invitees"`
}

type ReferralGPTRewardRecord struct {
	Id           int    `json:"id"`
	Role         string `json:"role"`
	Counterparty string `json:"counterparty"`
	TopupQuota   int64  `json:"topup_quota"`
	RewardQuota  int64  `json:"reward_quota"`
	GrantedAt    int64  `json:"granted_at"`
}

func referralGPTUSDToQuota(amount float64) int64 {
	if amount <= 0 || common.QuotaPerUnit <= 0 {
		return 0
	}
	return decimal.NewFromFloat(amount).
		Mul(decimal.NewFromFloat(common.QuotaPerUnit)).
		IntPart()
}

func EnsureReferralGPTRewardPlan() (*SubscriptionPlan, error) {
	referralGPTRewardPlanMu.Lock()
	defer referralGPTRewardPlanMu.Unlock()

	var plan SubscriptionPlan
	err := DB.Transaction(func(tx *gorm.DB) error {
		// This option is a stable cross-process lock row in production. Tests
		// that set the in-memory flag directly are serialized by the mutex.
		var optionLock Option
		lockResult := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("key = ?", "ReferralGPTRewardEnabled").Limit(1).Find(&optionLock)
		if lockResult.Error != nil {
			return lockResult.Error
		}

		lookup := tx.Where("plan_type = ?", SubscriptionPlanTypeGPTReferralReward).
			Order("id asc").First(&plan)
		if lookup.Error == nil {
			return nil
		}
		if !errors.Is(lookup.Error, gorm.ErrRecordNotFound) {
			return lookup.Error
		}

		plan = SubscriptionPlan{
			Title:              "APIMaster Referral GPT Credits",
			Subtitle:           "Permanent GPT credits earned from referrals",
			PlanType:           SubscriptionPlanTypeGPTReferralReward,
			PriceAmount:        0,
			Currency:           "USD",
			DurationUnit:       SubscriptionDurationYear,
			DurationValue:      1,
			Enabled:            true,
			SortOrder:          -100,
			MaxPurchasePerUser: 0,
			TotalAmount:        0,
			QuotaResetPeriod:   SubscriptionResetNever,
		}
		return tx.Create(&plan).Error
	})
	if err != nil {
		return nil, err
	}
	return &plan, nil
}

func lockReferralRewardUsersTx(tx *gorm.DB, inviteeId int) (*User, *User, error) {
	var inviteeSnapshot User
	if err := tx.Select("id", "inviter_id").Where("id = ?", inviteeId).First(&inviteeSnapshot).Error; err != nil {
		return nil, nil, err
	}
	if inviteeSnapshot.InviterId <= 0 || inviteeSnapshot.InviterId == inviteeSnapshot.Id {
		return &inviteeSnapshot, nil, nil
	}

	userIds := []int{inviteeSnapshot.Id, inviteeSnapshot.InviterId}
	sort.Ints(userIds)
	var lockedUsers []User
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Select("id", "inviter_id").Where("id IN ?", userIds).
		Order("id asc").Find(&lockedUsers).Error; err != nil {
		return nil, nil, err
	}
	if len(lockedUsers) != 2 {
		return nil, nil, errors.New("referral inviter or invitee not found")
	}

	var invitee, inviter *User
	for i := range lockedUsers {
		user := &lockedUsers[i]
		switch user.Id {
		case inviteeSnapshot.Id:
			invitee = user
		case inviteeSnapshot.InviterId:
			inviter = user
		}
	}
	if invitee == nil || inviter == nil || invitee.InviterId != inviter.Id {
		return nil, nil, errors.New("referral relationship changed during reward grant")
	}
	return invitee, inviter, nil
}

func grantReferralGPTQuotaTx(tx *gorm.DB, userId int, planId int, amount int64, now int64) (*UserSubscription, error) {
	if amount <= 0 {
		return nil, nil
	}
	var sub UserSubscription
	query := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("user_id = ? AND plan_id = ?", userId, planId).
		Order("id asc").Limit(1).Find(&sub)
	if query.Error != nil {
		return nil, query.Error
	}
	if query.RowsAffected == 0 {
		sub = UserSubscription{
			UserId:        userId,
			PlanId:        planId,
			AmountTotal:   amount,
			AmountUsed:    0,
			StartTime:     now,
			EndTime:       referralGPTRewardPermanentEndTime,
			Status:        "active",
			Source:        "referral",
			LastResetTime: now,
			NextResetTime: 0,
		}
		if err := tx.Create(&sub).Error; err != nil {
			return nil, err
		}
		return &sub, nil
	}
	if err := tx.Model(&sub).Updates(map[string]interface{}{
		"amount_total": gorm.Expr("amount_total + ?", amount),
		"end_time":     referralGPTRewardPermanentEndTime,
		"status":       "active",
		"updated_at":   now,
	}).Error; err != nil {
		return nil, err
	}
	if err := tx.Where("id = ?", sub.Id).First(&sub).Error; err != nil {
		return nil, err
	}
	return &sub, nil
}

func cumulativeReferralGPTTopupQuotaTx(tx *gorm.DB, userId int, currentQuota int64, tradeNo string) (int64, error) {
	if tx == nil || userId <= 0 || currentQuota <= 0 {
		return 0, nil
	}

	var currentTopup TopUp
	currentResult := tx.Where("user_id = ? AND trade_no = ?", userId, tradeNo).
		Limit(1).Find(&currentTopup)
	if currentResult.Error != nil {
		return 0, currentResult.Error
	}
	if currentResult.RowsAffected > 0 {
		if currentTopup.Status != common.TopUpStatusSuccess {
			return 0, nil
		}
		if common.ReferralGPTRewardStartTime > 0 && currentTopup.CompleteTime < common.ReferralGPTRewardStartTime {
			return 0, nil
		}
		currentQuota = int64(topUpCreditQuota(&currentTopup))
	}

	query := tx.Where("user_id = ? AND status = ? AND trade_no <> ?", userId, common.TopUpStatusSuccess, tradeNo)
	if currentResult.RowsAffected > 0 {
		query = query.Where(
			"complete_time < ? OR (complete_time = ? AND id < ?)",
			currentTopup.CompleteTime,
			currentTopup.CompleteTime,
			currentTopup.Id,
		)
	}
	if common.ReferralGPTRewardStartTime > 0 {
		query = query.Where("complete_time >= ?", common.ReferralGPTRewardStartTime)
	}
	var previousTopups []TopUp
	if err := query.Find(&previousTopups).Error; err != nil {
		return 0, err
	}
	cumulativeQuota := currentQuota
	for i := range previousTopups {
		cumulativeQuota += int64(topUpCreditQuota(&previousTopups[i]))
	}
	return cumulativeQuota, nil
}

// ProcessReferralGPTReward grants both sides once when the invitee's
// cumulative successful top-ups since campaign activation reach the threshold.
func ProcessReferralGPTReward(userId int, quotaAdded int, paymentMethod string, tradeNo string, source string) (*ReferralGPTRewardLog, error) {
	if !common.ReferralGPTRewardEnabled || userId <= 0 || quotaAdded <= 0 || strings.TrimSpace(tradeNo) == "" {
		return nil, nil
	}
	thresholdQuota := referralGPTUSDToQuota(common.ReferralGPTMinTopupUSD)
	if thresholdQuota <= 0 {
		return nil, nil
	}
	rewardQuota := referralGPTUSDToQuota(common.ReferralGPTRewardAmountUSD)
	if rewardQuota <= 0 {
		return nil, nil
	}
	if source == "" {
		source = "realtime"
	}

	plan, err := EnsureReferralGPTRewardPlan()
	if err != nil {
		return nil, err
	}
	now := GetDBTimestamp()
	var granted ReferralGPTRewardLog
	err = DB.Transaction(func(tx *gorm.DB) error {
		invitee, inviter, err := lockReferralRewardUsersTx(tx, userId)
		if err != nil {
			return err
		}
		if inviter == nil {
			return nil
		}

		var existing ReferralGPTRewardLog
		lookup := tx.Where("invitee_id = ? OR trade_no = ?", invitee.Id, tradeNo).
			Limit(1).Find(&existing)
		if lookup.Error != nil {
			return lookup.Error
		}
		if lookup.RowsAffected > 0 {
			return nil
		}
		cumulativeTopupQuota, err := cumulativeReferralGPTTopupQuotaTx(tx, invitee.Id, int64(quotaAdded), tradeNo)
		if err != nil {
			return err
		}
		if cumulativeTopupQuota < thresholdQuota {
			return nil
		}

		inviterSub, err := grantReferralGPTQuotaTx(tx, inviter.Id, plan.Id, rewardQuota, now)
		if err != nil {
			return err
		}
		inviteeSub, err := grantReferralGPTQuotaTx(tx, invitee.Id, plan.Id, rewardQuota, now)
		if err != nil {
			return err
		}

		granted = ReferralGPTRewardLog{
			TradeNo:            tradeNo,
			InviterId:          inviter.Id,
			InviteeId:          invitee.Id,
			TopupQuota:         cumulativeTopupQuota,
			InviterRewardQuota: rewardQuota,
			InviteeRewardQuota: rewardQuota,
			PaymentMethod:      paymentMethod,
			GrantSource:        source,
			GrantedAt:          now,
		}
		if inviterSub != nil {
			granted.InviterSubscriptionId = inviterSub.Id
		}
		if inviteeSub != nil {
			granted.InviteeSubscriptionId = inviteeSub.Id
		}
		return tx.Create(&granted).Error
	})
	if err != nil {
		return nil, err
	}
	if granted.Id == 0 {
		return nil, nil
	}
	return &granted, nil
}

func maskReferralEmail(value string) string {
	value = strings.TrimSpace(value)
	at := strings.Index(value, "@")
	if at <= 0 {
		return value
	}
	local := value[:at]
	if len(local) > 3 {
		local = local[:3] + "***"
	}
	return local + value[at:]
}

func referralRewardDisplayEmail(user *User, fallbackID int) string {
	if user != nil {
		if email := strings.TrimSpace(user.Email); email != "" {
			return email
		}
		if username := strings.TrimSpace(user.Username); username != "" {
			return username
		}
	}
	return fmt.Sprintf("user#%d", fallbackID)
}

func NotifyReferralGPTRewardToFeishu(logId int) error {
	if logId <= 0 {
		return nil
	}
	now := GetDBTimestamp()
	lockBefore := now - 300
	claim := DB.Model(&ReferralGPTRewardLog{}).
		Where("id = ? AND feishu_notified_at = 0 AND (feishu_notify_locked_at = 0 OR feishu_notify_locked_at < ?)", logId, lockBefore).
		Updates(map[string]interface{}{
			"feishu_notify_locked_at": now,
			"feishu_notify_attempts":  gorm.Expr("feishu_notify_attempts + 1"),
		})
	if claim.Error != nil || claim.RowsAffected == 0 {
		return claim.Error
	}

	var reward ReferralGPTRewardLog
	if err := DB.Where("id = ?", logId).First(&reward).Error; err != nil {
		return err
	}
	var inviter, invitee User
	_ = DB.Select("id", "email", "username").Where("id = ?", reward.InviterId).First(&inviter).Error
	_ = DB.Select("id", "email", "username").Where("id = ?", reward.InviteeId).First(&invitee).Error

	formatQuotaUSD := func(quota int64) string {
		if common.QuotaPerUnit <= 0 {
			return "$0.00"
		}
		return fmt.Sprintf("$%.2f", float64(quota)/common.QuotaPerUnit)
	}
	lines := []string{
		fmt.Sprintf("时间：%s (CST)", time.Unix(reward.GrantedAt, 0).In(time.FixedZone("CST", 8*3600)).Format("2006-01-02 15:04:05")),
		fmt.Sprintf("被邀请人：%s（ID: %d）", referralRewardDisplayEmail(&invitee, reward.InviteeId), reward.InviteeId),
		fmt.Sprintf("邀请人：%s（ID: %d）", referralRewardDisplayEmail(&inviter, reward.InviterId), reward.InviterId),
		fmt.Sprintf("累计充值到账：%s", formatQuotaUSD(reward.TopupQuota)),
		fmt.Sprintf("被邀请人奖励：%s GPT 额度", formatQuotaUSD(reward.InviteeRewardQuota)),
		fmt.Sprintf("邀请人奖励：%s GPT 额度", formatQuotaUSD(reward.InviterRewardQuota)),
		fmt.Sprintf("交易单号：%s", reward.TradeNo),
		fmt.Sprintf("触发方式：%s", reward.GrantSource),
	}
	// Referral reward notices are operational/business notifications and should
	// stay in the main ops Feishu group instead of the channel alert group.
	if err := common.SendFeishuCard(common.FeishuOpsChatID(), "🎁 邀请首充 GPT 奖励已发放", lines); err != nil {
		DB.Model(&ReferralGPTRewardLog{}).Where("id = ?", logId).Update("feishu_notify_locked_at", 0)
		return err
	}
	return DB.Model(&ReferralGPTRewardLog{}).Where("id = ?", logId).Updates(map[string]interface{}{
		"feishu_notified_at":      GetDBTimestamp(),
		"feishu_notify_locked_at": 0,
	}).Error
}

func RetryPendingReferralGPTRewardNotifications(limit int) error {
	if limit <= 0 {
		limit = 100
	}
	var ids []int
	if err := DB.Model(&ReferralGPTRewardLog{}).
		Where("feishu_notified_at = 0").Order("id asc").Limit(limit).Pluck("id", &ids).Error; err != nil {
		return err
	}
	for _, id := range ids {
		_ = NotifyReferralGPTRewardToFeishu(id)
	}
	return nil
}

func GetReferralGPTRewardSummary(userId int) (*ReferralGPTRewardSummary, error) {
	summary := &ReferralGPTRewardSummary{
		Enabled:     common.ReferralGPTRewardEnabled,
		MinTopupUSD: common.ReferralGPTMinTopupUSD,
		RewardUSD:   common.ReferralGPTRewardAmountUSD,
	}
	if userId <= 0 {
		return summary, nil
	}
	var plans []SubscriptionPlan
	if err := DB.Select("id").Where("plan_type = ?", SubscriptionPlanTypeGPTReferralReward).Find(&plans).Error; err != nil {
		return nil, err
	}
	planIds := make([]int, 0, len(plans))
	for _, plan := range plans {
		planIds = append(planIds, plan.Id)
	}
	if len(planIds) > 0 {
		var totals struct {
			AmountTotal int64
			AmountUsed  int64
		}
		if err := DB.Model(&UserSubscription{}).
			Where("user_id = ? AND plan_id IN ?", userId, planIds).
			Select("COALESCE(SUM(amount_total), 0) AS amount_total, COALESCE(SUM(amount_used), 0) AS amount_used").
			Scan(&totals).Error; err != nil {
			return nil, err
		}
		summary.RemainingQuota = totals.AmountTotal - totals.AmountUsed
		if summary.RemainingQuota < 0 {
			summary.RemainingQuota = 0
		}
	}
	if err := DB.Model(&ReferralGPTRewardLog{}).
		Where("inviter_id = ?", userId).Count(&summary.QualifiedInvitees).Error; err != nil {
		return nil, err
	}
	if err := DB.Model(&ReferralGPTRewardLog{}).
		Where("inviter_id = ? OR invitee_id = ?", userId, userId).
		Select("COALESCE(SUM(CASE WHEN inviter_id = ? THEN inviter_reward_quota ELSE invitee_reward_quota END), 0)", userId).
		Scan(&summary.CumulativeQuota).Error; err != nil {
		return nil, err
	}
	return summary, nil
}

func GetReferralGPTRewardLogs(userId, page, pageSize int) ([]ReferralGPTRewardRecord, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	query := DB.Model(&ReferralGPTRewardLog{}).Where("inviter_id = ? OR invitee_id = ?", userId, userId)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var logs []ReferralGPTRewardLog
	if err := query.Order("granted_at desc, id desc").Offset((page - 1) * pageSize).Limit(pageSize).Find(&logs).Error; err != nil {
		return nil, 0, err
	}
	userIds := make([]int, 0, len(logs))
	for _, item := range logs {
		if item.InviterId == userId {
			userIds = append(userIds, item.InviteeId)
		} else {
			userIds = append(userIds, item.InviterId)
		}
	}
	var users []User
	if len(userIds) > 0 {
		_ = DB.Select("id", "username").Where("id IN ?", userIds).Find(&users).Error
	}
	usernames := make(map[int]string, len(users))
	for _, user := range users {
		usernames[user.Id] = maskReferralEmail(user.Username)
	}
	records := make([]ReferralGPTRewardRecord, 0, len(logs))
	for _, item := range logs {
		record := ReferralGPTRewardRecord{Id: item.Id, TopupQuota: item.TopupQuota, GrantedAt: item.GrantedAt}
		if item.InviterId == userId {
			record.Role = "inviter"
			record.Counterparty = usernames[item.InviteeId]
			record.RewardQuota = item.InviterRewardQuota
		} else {
			record.Role = "invitee"
			record.Counterparty = usernames[item.InviterId]
			record.RewardQuota = item.InviteeRewardQuota
		}
		records = append(records, record)
	}
	return records, total, nil
}

func GetReferralGPTRewardedInvitees(inviteeIds []int) (map[int]int64, error) {
	result := make(map[int]int64)
	if len(inviteeIds) == 0 {
		return result, nil
	}
	var logs []ReferralGPTRewardLog
	if err := DB.Select("invitee_id", "granted_at").Where("invitee_id IN ?", inviteeIds).Find(&logs).Error; err != nil {
		return nil, err
	}
	for _, item := range logs {
		result[item.InviteeId] = item.GrantedAt
	}
	return result, nil
}

func ReconcileReferralGPTRewards(limit int) error {
	if !common.ReferralGPTRewardEnabled || common.ReferralGPTRewardStartTime <= 0 {
		return nil
	}
	if limit <= 0 {
		limit = 1000
	}
	lowerBound := common.ReferralGPTRewardStartTime
	if recent := GetDBTimestamp() - 48*3600; recent > lowerBound {
		lowerBound = recent
	}
	var topups []TopUp
	if err := DB.Where("status = ? AND complete_time >= ?", common.TopUpStatusSuccess, lowerBound).
		Order("complete_time desc, id desc").Limit(limit).Find(&topups).Error; err != nil {
		return err
	}
	for _, topup := range topups {
		quota := int(topUpCreditQuota(&topup))
		granted, err := ProcessReferralGPTReward(topup.UserId, quota, topup.PaymentMethod, topup.TradeNo, "reconcile")
		if err != nil {
			common.SysLog(fmt.Sprintf("referral GPT reward reconcile failed trade_no=%s: %v", topup.TradeNo, err))
			continue
		}
		if granted != nil {
			_ = NotifyReferralGPTRewardToFeishu(granted.Id)
		}
	}
	return nil
}
