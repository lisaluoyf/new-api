package controller

import (
	"context"
	"errors"
	"math"
	"net/http"
	"regexp"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

var queryPlategaStatus = service.GetPlategaTransactionStatus

func processPlategaEvent(ctx context.Context, event *model.PlategaEvent) error {
	if event.Status == "done" {
		return nil
	}
	// An order's local callback sequence cannot establish provider history. Every
	// decision below uses an authenticated, identity-checked current API result.
	order := model.GetPlategaOrderByTransactionId(event.TransactionID)
	if order == nil {
		err := model.ErrTopUpNotFound
		_ = model.RetryPlategaEvent(event.Id, "payment_order_missing")
		return err
	}
	status, err := queryPlategaStatus(ctx, order.PlategaTransactionId)
	if err == nil {
		err = service.ValidatePlategaAPIOrder(status, order)
	}
	if err != nil {
		_ = model.RetryPlategaEvent(event.Id, "trusted_API_verification_failed")
		return err
	}
	proof := model.VerifiedPlategaState{TransactionID: order.PlategaTransactionId, TradeNo: order.TradeNo, UserID: order.UserId, Status: status.Status, APIJSON: status.RawJSON}
	// Platega supports whole-transaction refunds only (operator confirmed
	// 2026-10-08). The callback amount includes fees; reverse the frozen base
	// and original granted quota only after the authenticated API says completed.
	if model.NormalizePlategaAPIStatus(status.Status) == model.PlategaStatusChargeback && status.RefundStatus != nil && *status.RefundStatus == "COMPLETED" {
		base := order.RubAmount
		proof.CumulativeReversalRub = &base
		proof.ReversalEvidence = "authenticated Platega API: CHARGEBACKED/COMPLETED; operator-confirmed whole-transaction refund policy (2026-10-08)"
	}
	if event.Source == "admin-refund-evidence" {
		var evidence plategaRefundEvidenceRequest
		if common.UnmarshalJsonStr(event.PayloadJSON, &evidence) != nil || !validPlategaRefundEvidence(evidence) || model.NormalizePlategaAPIStatus(status.Status) != model.PlategaStatusChargeback {
			_ = model.RetryPlategaEvent(event.Id, "refund_evidence_conflicts_with_official_funds_state")
			return errors.New("refund evidence or official status mismatch")
		}
		proof.CumulativeReversalRub = &evidence.CumulativeBaseRefundedRub
		proof.ReversalEvidence = evidence.EvidenceReference + " sha256:" + evidence.EvidenceSHA256
	}
	// Incomplete or inconsistent refund states must not trigger an automatic
	// reversal. Explicit historical receipt evidence remains supported above.
	if status.RefundStatus != nil && *status.RefundStatus != "" && model.NormalizePlategaAPIStatus(status.Status) != model.PlategaStatusChargeback {
		_ = model.RetryPlategaEvent(event.Id, "refund_status_requires_funds_review")
		return nil
	}
	if err = model.ApplyVerifiedPlategaEvent(event.Id, proof); err != nil {
		_ = model.RetryPlategaEvent(event.Id, "business_transaction_failed")
		return err
	}
	_ = model.FinishPlategaEventEffects(event.Id)
	return nil
}

// The documented query has no refunded amount. A root operator can supply a
// separately verified provider receipt; its hash/reference and actor are saved
// with the immutable event. This endpoint never initiates another refund.
type plategaRefundEvidenceRequest struct {
	TransactionID             string  `json:"transaction_id"`
	TradeNo                   string  `json:"trade_no"`
	CumulativeBaseRefundedRub float64 `json:"cumulative_base_refunded_rub"`
	EvidenceReference         string  `json:"evidence_reference"`
	EvidenceSHA256            string  `json:"evidence_sha256"`
}

var plategaReceiptHashPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

func validPlategaRefundEvidence(e plategaRefundEvidenceRequest) bool {
	return e.TransactionID != "" && e.TradeNo != "" && e.CumulativeBaseRefundedRub > 0 && !math.IsNaN(e.CumulativeBaseRefundedRub) && !math.IsInf(e.CumulativeBaseRefundedRub, 0) && len(e.EvidenceReference) > 0 && len(e.EvidenceReference) <= 256 && plategaReceiptHashPattern.MatchString(e.EvidenceSHA256)
}
func AdminApplyPlategaRefundEvidence(c *gin.Context) {
	var evidence plategaRefundEvidenceRequest
	if c.ShouldBindJSON(&evidence) != nil || !validPlategaRefundEvidence(evidence) {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "verified provider receipt and cumulative base refund amount required"})
		return
	}
	order := model.GetPlategaOrderByTransactionId(evidence.TransactionID)
	if order == nil || order.TradeNo != evidence.TradeNo || evidence.CumulativeBaseRefundedRub > order.RubAmount {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "refund identity or amount conflict"})
		return
	}
	payload, _ := common.Marshal(evidence)
	event, err := model.SavePlategaEvent(order.TradeNo, order.PlategaTransactionId, "admin-refund-evidence", c.ClientIP(), string(payload), "", c.GetInt("id"))
	if err == nil {
		err = processPlategaEvent(c.Request.Context(), event)
	}
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "message": "not applied; verified retry retained"})
		return
	}
	var saved model.PlategaEvent
	if model.DB.First(&saved, event.Id).Error != nil {
		c.Status(http.StatusInternalServerError)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": saved.Status == "done", "message": saved.Status, "data": saved})
}

func StartPlategaReconcileTask() {
	if !common.IsMasterNode {
		return
	}
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			if service.PlategaConfigured() {
				var effects []model.PlategaEvent
				if model.DB.Where("effects_pending = ?", true).Limit(30).Find(&effects).Error == nil {
					for _, e := range effects {
						_ = model.FinishPlategaEventEffects(e.Id)
					}
				}
				var events []model.PlategaEvent
				if err := model.DB.Where("status IN ? AND next_attempt <= ?", []string{"received", "retry", "recheck", "review"}, common.GetTimestamp()).Order("id asc").Limit(30).Find(&events).Error; err == nil {
					for i := range events {
						ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
						_ = processPlategaEvent(ctx, &events[i])
						cancel()
					}
				}
			}
			<-ticker.C
		}
	}()
}
