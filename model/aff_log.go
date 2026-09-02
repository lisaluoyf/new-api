package model

import (
	"fmt"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

type AffLog struct {
	Id          int   `json:"id" gorm:"primaryKey;autoIncrement"`
	InviterId   int   `json:"inviter_id" gorm:"index;not null"`
	InviteeId   int   `json:"invitee_id" gorm:"not null"`
	TopupAmount int   `json:"topup_amount"` // 实付美元折算的额度（quota 单位）
	Commission  int   `json:"commission"`   // 返佣额（quota 单位）
	CreatedAt   int64 `json:"created_at"`
}

func (AffLog) TableName() string {
	return "aff_logs"
}

func resolveAffCommissionRatio(user *User) int {
	if user == nil || user.InviterId == 0 {
		return 0
	}
	if user.AffRatioSnapshot != nil {
		return *user.AffRatioSnapshot
	}
	return common.AffRatio
}

func paidQuotaForAffCommission(userId int, tradeNo string) (int, error) {
	tradeNo = strings.TrimSpace(tradeNo)
	if userId <= 0 || tradeNo == "" {
		return 0, fmt.Errorf("invalid user or trade number")
	}
	topUp := GetTopUpByTradeNo(tradeNo)
	if topUp == nil {
		return 0, fmt.Errorf("top-up not found")
	}
	if topUp.UserId != userId {
		return 0, fmt.Errorf("top-up user mismatch")
	}
	if topUp.Status != common.TopUpStatusSuccess {
		return 0, fmt.Errorf("top-up is not successful")
	}
	if topUp.PaidAmountUSD <= 0 {
		return 0, fmt.Errorf("missing paid_amount_usd")
	}
	paidQuota := decimal.NewFromFloat(topUp.PaidAmountUSD).
		Mul(decimal.NewFromFloat(common.QuotaPerUnit)).
		Round(0).
		IntPart()
	if paidQuota <= 0 {
		return 0, fmt.Errorf("invalid paid quota")
	}
	return int(paidQuota), nil
}

// ProcessAffCommissionForTopUp resolves the commission base from the order's
// immutable paid USD snapshot. Users without an inviter do not need an order
// snapshot because no affiliate commission can be generated for them.
func ProcessAffCommissionForTopUp(userId int, tradeNo string) error {
	user, err := GetUserById(userId, false)
	if err != nil {
		return fmt.Errorf("load invitee: %w", err)
	}
	if user == nil || user.InviterId == 0 || resolveAffCommissionRatio(user) <= 0 {
		return nil
	}
	paidQuota, err := paidQuotaForAffCommission(userId, tradeNo)
	if err != nil {
		return err
	}
	ProcessAffCommission(userId, paidQuota)
	return nil
}

// ProcessAffCommission 在充值成功后调用，按实付美元折算的 quota 给邀请者加返佣。
func ProcessAffCommission(userId int, paidQuota int) {
	user, err := GetUserById(userId, false)
	if err != nil || user == nil || user.InviterId == 0 {
		return
	}

	ratio := resolveAffCommissionRatio(user)
	if ratio <= 0 {
		return
	}

	commission := paidQuota * ratio / 100
	if commission <= 0 {
		return
	}

	// 邀请者：加到待划转池（aff_quota + aff_history）。被邀请者无奖励。
	err = DB.Model(&User{}).Where("id = ?", user.InviterId).Updates(map[string]interface{}{
		"aff_quota":   gorm.Expr("aff_quota + ?", commission),
		"aff_history": gorm.Expr("aff_history + ?", commission),
	}).Error
	if err != nil {
		common.SysLog("ProcessAffCommission: failed to update inviter quota: " + err.Error())
		return
	}

	// 写记录
	log := &AffLog{
		InviterId:   user.InviterId,
		InviteeId:   userId,
		TopupAmount: paidQuota,
		Commission:  commission,
		CreatedAt:   time.Now().Unix(),
	}
	if err = DB.Create(log).Error; err != nil {
		common.SysLog("ProcessAffCommission: failed to insert aff_log: " + err.Error())
	}
}

// GetAffLogs 查询邀请者的返佣记录，分页。
func GetAffLogs(inviterId int, page, pageSize int) (logs []AffLog, total int64, err error) {
	query := DB.Model(&AffLog{}).Where("inviter_id = ?", inviterId)
	query.Count(&total)
	err = query.Order("created_at DESC").
		Offset((page - 1) * pageSize).
		Limit(pageSize).
		Find(&logs).Error
	return
}

// GetInviteList 查询邀请者邀请的用户列表。
func GetInviteList(inviterId int, page, pageSize int) (users []User, total int64, err error) {
	query := DB.Model(&User{}).Where("inviter_id = ?", inviterId).Select("id, username, display_name, created_at")
	query.Count(&total)
	err = query.Order("created_at DESC").
		Offset((page - 1) * pageSize).
		Limit(pageSize).
		Find(&users).Error
	return
}
