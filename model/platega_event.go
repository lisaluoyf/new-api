package model

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Immutable authenticated input plus durable processing state. Audit and
// monetary/entitlement changes commit in the same primary database transaction.
type PlategaEvent struct {
	Id                int    `json:"id"`
	EventKey          string `json:"event_key" gorm:"uniqueIndex;type:varchar(64)"`
	TradeNo           string `json:"trade_no" gorm:"index;type:varchar(255)"`
	TransactionID     string `json:"transaction_id" gorm:"type:varchar(255)"`
	Source            string `json:"source" gorm:"type:varchar(32)"`
	ActorID           int    `json:"actor_id"`
	SourceIP          string `json:"source_ip" gorm:"type:varchar(64)"`
	PayloadJSON       string `json:"payload_json" gorm:"type:text"`
	APIJSON           string `json:"api_json" gorm:"type:text"`
	APIStatus         string `json:"api_status" gorm:"type:varchar(32)"`
	CallbackStatus    string `json:"callback_status" gorm:"type:varchar(32)"`
	Status            string `json:"status" gorm:"index;type:varchar(32)"`
	Attempts          int    `json:"attempts"`
	NextAttempt       int64  `json:"next_attempt" gorm:"index"`
	Error             string `json:"error" gorm:"type:text"`
	BeforeStateJSON   string `json:"before_state_json" gorm:"type:text"`
	AfterStateJSON    string `json:"after_state_json" gorm:"type:text"`
	QuotaDelta        int64  `json:"quota_delta"`
	EntitlementAction string `json:"entitlement_action" gorm:"type:text"`
	CreatedAt         int64  `json:"created_at"`
	ProcessedAt       int64  `json:"processed_at"`
	EffectsPending    bool   `json:"effects_pending" gorm:"not null;default:false"`
	EffectQuota       int64  `json:"effect_quota" gorm:"not null;default:0"`
}

// Each verification attempt remains an append-only audit entry, including
// conflicts and transient failures; retries cannot erase earlier evidence.
type PlategaObservation struct {
	Id                int
	EventID           int `gorm:"index"`
	ObservedAt        int64
	Status            string `gorm:"type:varchar(32)"`
	APIJSON           string `gorm:"type:text"`
	Error             string `gorm:"type:text"`
	BeforeStateJSON   string `gorm:"type:text"`
	AfterStateJSON    string `gorm:"type:text"`
	QuotaDelta        int64
	EntitlementAction string `gorm:"type:text"`
}

func SavePlategaEvent(trade, tid, source, ip, payload, status string, actor int) (*PlategaEvent, error) {
	key := fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%s\n%d\n%s\n%s", source, actor, tid, payload))))
	e := &PlategaEvent{EventKey: key, TradeNo: trade, TransactionID: tid, Source: source, ActorID: actor, SourceIP: ip, PayloadJSON: payload, CallbackStatus: NormalizePlategaAPIStatus(status), Status: "received", CreatedAt: common.GetTimestamp()}
	if err := DB.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "event_key"}}, DoNothing: true}).Create(e).Error; err != nil {
		return nil, err
	}
	if err := DB.Where("event_key = ?", key).First(e).Error; err != nil {
		return nil, err
	}
	return e, nil
}

func RetryPlategaEvent(id int, reason string) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&PlategaEvent{}).Where("id = ? AND status <> ?", id, "done").Updates(map[string]any{"status": "retry", "error": reason, "next_attempt": common.GetTimestamp() + 60, "attempts": gorm.Expr("attempts + 1")}).Error; err != nil {
			return err
		}
		return tx.Create(&PlategaObservation{EventID: id, ObservedAt: common.GetTimestamp(), Status: "retry", Error: reason}).Error
	})
}

// CumulativeReversalRub is base money actually returned, NOT the invoice
// amount or the local order's price. Missing external refund amount requires
// review. The reconciler never invents a full refund from a status alone.
type VerifiedPlategaState struct {
	TransactionID         string
	TradeNo               string
	UserID                int
	Status                string
	APIJSON               string
	CumulativeReversalRub *float64
	ReversalEvidence      string
}

func ApplyVerifiedPlategaEvent(eventID int, proof VerifiedPlategaState) error {
	var invalidateID int
	returnErr := DB.Transaction(func(tx *gorm.DB) error {
		var event PlategaEvent
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&event, eventID).Error; err != nil {
			return err
		}
		if event.Status == "done" {
			return nil
		}
		var order PlategaOrder
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("trade_no = ?", event.TradeNo).First(&order).Error; err != nil {
			return err
		}
		if proof.TransactionID != order.PlategaTransactionId || event.TransactionID != order.PlategaTransactionId || proof.TradeNo != order.TradeNo || proof.UserID != order.UserId || proof.APIJSON == "" {
			return errors.New("verified payment identity mismatch")
		}
		var top TopUp
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("trade_no = ?", order.TradeNo).First(&top).Error; err != nil {
			return err
		}
		if top.PaymentProvider != PaymentProviderPlatega || top.UserId != order.UserId {
			return ErrPaymentMethodMismatch
		}
		var sub SubscriptionOrder
		subQuery := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("trade_no = ?", order.TradeNo).Limit(1).Find(&sub)
		if subQuery.Error != nil {
			return subQuery.Error
		}
		isSubscription := subQuery.RowsAffected > 0
		if isSubscription && (sub.PaymentProvider != PaymentProviderPlatega || sub.UserId != order.UserId) {
			return ErrPaymentMethodMismatch
		}
		var user User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&user, order.UserId).Error; err != nil {
			return err
		}
		snapshotOrder := order
		snapshotOrder.CallbackHeadersJSON = `{"authentication":"historical_headers_redacted"}`
		before, _ := common.Marshal(map[string]any{"payment": snapshotOrder, "topup": top, "subscription_order": sub, "wallet_quota": user.Quota})
		event.BeforeStateJSON = string(before)
		event.APIJSON = proof.APIJSON
		event.APIStatus = proof.Status
		event.Attempts++
		event.Error = ""
		event.Status = "done"
		event.QuotaDelta = 0
		order.ReviewReason = ""
		review := func(reason string) {
			event.Status = "review"
			event.Error = reason
			order.ReviewReason = reason
			event.NextAttempt = common.GetTimestamp() + 300
		}
		state := NormalizePlategaAPIStatus(proof.Status)
		switch state {
		case PlategaStatusConfirmed:
			if order.ReversedQuota > 0 || order.ReversedRub > 0 || top.Status == common.TopUpStatusRefunded || (isSubscription && (sub.RefundAmount > 0 || sub.ChargebackAmount > 0)) {
				review("confirmed_after_reversal_requires_verified_reinstatement")
				break
			}
			if top.Status != common.TopUpStatusSuccess {
				if top.Status != common.TopUpStatusPending && top.Status != common.TopUpStatusFailed && top.Status != "expired" && top.Status != "canceled" {
					review("unsupported_business_state")
					break
				}
				if isSubscription {
					if sub.Status != common.TopUpStatusSuccess {
						// Completion helpers keep existing purchase/renewal/upgrade guards;
						// a delayed payment never overwrites another active subscription.
						if err := tx.Model(&sub).Update("status", common.TopUpStatusPending).Error; err != nil {
							return err
						}
						if err := completeSubscriptionOrderWithDB(tx, order.TradeNo, proof.APIJSON, PaymentProviderPlatega, PaymentMethodPlatega, false); err != nil {
							return err
						}
						var entitlement UserSubscription
						if err := tx.Where("user_id = ? AND current_cycle_id = ?", order.UserId, sub.Id).First(&entitlement).Error; err != nil {
							return err
						}
						order.GrantedSubscriptionID = entitlement.Id
						event.EntitlementAction = "issued_subscription"
					} else if err := upsertSubscriptionTopUpTx(tx, &sub); err != nil {
						return err
					}
				} else {
					if order.GrantedQuota > 0 {
						// An audited issuance survives an accidental business-state change.
						MarkTopUpSuccess(&top)
						if err := tx.Save(&top).Error; err != nil {
							return err
						}
					} else {
						quota := decimal.NewFromFloat(topUpCreditQuota(&top)).Round(0).IntPart()
						if quota <= 0 || (user.Quota > 0 && quota > math.MaxInt64-int64(user.Quota)) {
							return errors.New("invalid issued quota")
						}
						top.CompleteTime = common.GetTimestamp()
						MarkTopUpSuccess(&top)
						if err := tx.Save(&top).Error; err != nil {
							return err
						}
						if result := tx.Model(&User{}).Where("id = ?", order.UserId).Update("quota", gorm.Expr("quota + ?", quota)); result.Error != nil {
							return result.Error
						} else if result.RowsAffected != 1 {
							return errors.New("wallet owner missing")
						}
						order.GrantedQuota = quota
						event.QuotaDelta = quota
						invalidateID = order.UserId
					}
				}
			}
			order.PlategaStatus = PlategaStatusConfirmed
			if event.CallbackStatus != "" && event.CallbackStatus != state {
				event.Status = "recheck"
				event.Error = "callback_conflicts_with_authoritative_API"
				event.NextAttempt = common.GetTimestamp() + 300
			}
		case PlategaStatusCanceled:
			order.PlategaStatus = PlategaStatusCanceled
			if top.Status == common.TopUpStatusSuccess || (isSubscription && sub.Status == common.TopUpStatusSuccess) {
				// Cancellation alone never proves money was returned or original issuance
				// was erroneous. Keep entitlement and create a recoverable exception.
				review("canceled_after_issuance_requires_first_success_and_funds_review")
			} else {
				if top.Status == common.TopUpStatusPending {
					if err := tx.Model(&top).Updates(map[string]any{"status": common.TopUpStatusFailed, "complete_time": common.GetTimestamp()}).Error; err != nil {
						return err
					}
				}
				if isSubscription && sub.Status == common.TopUpStatusPending {
					if err := tx.Model(&sub).Updates(map[string]any{"status": "expired", "complete_time": common.GetTimestamp()}).Error; err != nil {
						return err
					}
				}
				order.PlategaStatus = PlategaStatusCanceled
			}
		case PlategaStatusChargeback:
			if proof.CumulativeReversalRub == nil || strings.TrimSpace(proof.ReversalEvidence) == "" {
				review("returned_funds_amount_unverified")
				break
			}
			amount := decimal.NewFromFloat(*proof.CumulativeReversalRub)
			base := decimal.NewFromFloat(order.RubAmount)
			if !amount.IsPositive() || amount.GreaterThan(base) || !amount.Equal(amount.Truncate(2)) {
				return errors.New("invalid cumulative refund base amount")
			}
			if amount.LessThan(decimal.NewFromFloat(order.ReversedRub)) {
				review("out_of_order_refund_total")
				break
			}
			if isSubscription {
				if !amount.Equal(base) {
					review("partial_subscription_refund_requires_entitlement_review")
					break
				}
				var current UserSubscription
				if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("user_id = ? AND current_cycle_id = ?", order.UserId, sub.Id).First(&current).Error; err != nil {
					review("subscription_lineage_changed_requires_review")
					break
				}
				var descendants int64
				if err := tx.Model(&SubscriptionOrder{}).Where("previous_subscription_id = ? AND status = ? AND id <> ?", current.Id, common.TopUpStatusSuccess, sub.Id).Count(&descendants).Error; err != nil {
					return err
				}
				if descendants > 0 {
					review("subscription_renewed_or_upgraded_requires_review")
					break
				}
				if order.ReversedRub == order.RubAmount {
					break
				}
				if err := reverseSubscriptionOrderWithDB(tx, order.TradeNo, sub.Money, "chargeback", proof.ReversalEvidence); err != nil {
					return err
				}
				event.EntitlementAction = "revoked_subscription_preserved_usage"
				order.GrantedSubscriptionID = current.Id
			} else {
				if top.Status != common.TopUpStatusSuccess && top.Status != common.TopUpStatusRefunded {
					review("refund_without_proven_issuance")
					break
				}
				original := order.GrantedQuota
				if original == 0 {
					// Legacy RechargePlatega credited Amount, even when presentation metadata
					// used CreditedAmount. Freeze what that implementation actually issued.
					original = decimal.NewFromInt(top.Amount).Mul(decimal.NewFromFloat(common.QuotaPerUnit)).IntPart()
					if original <= 0 {
						return errors.New("legacy issued quota unavailable")
					}
					order.GrantedQuota = original
				}
				recovered := order.ReversedQuota
				if int64(top.RefundedQuota) > recovered {
					recovered = int64(top.RefundedQuota)
				}
				target := decimal.NewFromInt(original).Mul(amount).Div(base).Round(0).IntPart()
				delta := target - recovered
				if delta < 0 {
					review("refund_total_below_already_recovered_quota")
					break
				}
				if delta > 0 {
					if int64(user.Quota) < math.MinInt64+delta {
						return errors.New("wallet debt exceeds supported precision")
					}
					if err := tx.Model(&User{}).Where("id = ?", order.UserId).Update("quota", gorm.Expr("quota - ?", delta)).Error; err != nil {
						return err
					}
					event.QuotaDelta = -delta
					invalidateID = order.UserId
				}
				order.ReversedQuota = target
				top.RefundedQuota = int(target)
				top.RefundedAmount = decimal.NewFromFloat(top.Money).Mul(amount).Div(base).Round(6).InexactFloat64()
				if amount.Equal(base) {
					top.Status = common.TopUpStatusRefunded
				}
				if err := tx.Save(&top).Error; err != nil {
					return err
				}
			}
			order.ReversedRub = amount.InexactFloat64()
			order.PlategaStatus = PlategaStatusChargeback
		case PlategaStatusPending:
			event.Status = "recheck"
			event.Error = "provider_payment_not_final"
			event.NextAttempt = common.GetTimestamp() + 300
		default:
			review("unknown_provider_status")
		}
		order.UpdateTime = common.GetTimestamp()
		if event.Source == "callback" {
			order.CallbackJSON = event.PayloadJSON
			order.CallbackHeadersJSON = `{"authentication":"merchant_and_secret_verified"}`
		}
		if err := tx.Save(&order).Error; err != nil {
			return err
		}
		var afterTop TopUp
		var afterUser User
		if err := tx.Where("trade_no = ?", order.TradeNo).First(&afterTop).Error; err != nil {
			return err
		}
		if err := tx.First(&afterUser, order.UserId).Error; err != nil {
			return err
		}
		snapshotOrder = order
		snapshotOrder.CallbackHeadersJSON = `{"authentication":"headers_redacted"}`
		after, _ := common.Marshal(map[string]any{"payment": snapshotOrder, "topup": afterTop, "wallet_quota": afterUser.Quota})
		event.AfterStateJSON = string(after)
		event.ProcessedAt = common.GetTimestamp()
		if event.QuotaDelta > 0 {
			event.EffectsPending = true
			event.EffectQuota = event.QuotaDelta
		}
		if event.EntitlementAction == "issued_subscription" && event.EffectQuota == 0 {
			event.EffectsPending = true
			event.EffectQuota = decimal.NewFromFloat(sub.Money).Mul(decimal.NewFromFloat(common.QuotaPerUnit)).Round(0).IntPart()
		}
		if err := tx.Create(&PlategaObservation{EventID: event.Id, ObservedAt: event.ProcessedAt, Status: event.Status, APIJSON: event.APIJSON, Error: event.Error, BeforeStateJSON: event.BeforeStateJSON, AfterStateJSON: event.AfterStateJSON, QuotaDelta: event.QuotaDelta, EntitlementAction: event.EntitlementAction}).Error; err != nil {
			return err
		}
		return tx.Save(&event).Error
	})
	if returnErr == nil && invalidateID > 0 {
		_ = invalidateUserCache(invalidateID)
	}
	return returnErr
}

func FinishPlategaEventEffects(id int) error {
	var e PlategaEvent
	if err := DB.First(&e, id).Error; err != nil {
		return err
	}
	if !e.EffectsPending {
		return nil
	}
	var order PlategaOrder
	if err := DB.Where("trade_no = ?", e.TradeNo).First(&order).Error; err != nil {
		return err
	}
	_ = invalidateUserCache(order.UserId)
	OnTopupSucceeded(order.UserId, int(e.EffectQuota), PaymentMethodPlatega, order.TradeNo)
	return DB.Model(&e).Update("effects_pending", false).Error
}
