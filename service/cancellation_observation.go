package service

import (
	"crypto/sha256"
	"fmt"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
)

// ObserveCanceledRelay only snapshots metadata already known at cancellation.
// Do not serialize RelayInfo: it contains customer bodies and credential keys.
func ObserveCanceledRelay(c *gin.Context, info *relaycommon.RelayInfo) error {
	if c == nil || info == nil {
		return fmt.Errorf("missing cancellation context")
	}
	requestID := info.RequestId
	if requestID == "" {
		requestID = c.GetString(common.RequestIdKey)
	}
	item := &model.CancellationObservation{
		RequestId: requestID, UserId: info.UserId, TokenId: info.TokenId,
		ChannelId: c.GetInt("channel_id"), ModelName: info.OriginModelName,
		Source: "live_cancel", CreatedAt: common.GetTimestamp(), IsStream: info.IsStream,
		ReceivedResponses: info.ReceivedResponseCount, SentResponses: info.SendResponseCount,
		UpstreamRequestId: info.ObservationUpstreamRequestID,
	}
	if !info.StartTime.IsZero() {
		item.StartedAt = info.StartTime.UnixMilli()
		item.DurationMS = time.Since(info.StartTime).Milliseconds()
	}
	if info.StreamStatus != nil {
		_, item.UpstreamResponseId = info.StreamStatus.TerminalUsage()
	}
	preConsumed := 0
	if info.Billing != nil {
		preConsumed = info.Billing.GetPreConsumedQuota()
	}
	snapshot := map[string]interface{}{
		"version": 1, "mode": "observe_only", "billing_source": info.BillingSource,
		"subscription_id": info.SubscriptionId, "subscription_plan_id": info.SubscriptionPlanId,
		"subscription_cycle_id":  info.SubscriptionCycleId,
		"subscription_plan_type": info.SubscriptionPlanType,
		"price_data":             info.PriceData, "price_data_source": info.PriceDataSource,
		"tiered_snapshot": info.TieredBillingSnapshot,
		"group":           info.UsingGroup, "preconsumed_quota": preConsumed,
		"request_path": info.RequestURLPath, "relay_format": info.RelayFormat,
		"used_channels": c.GetStringSlice("use_channel"), "attempt_index": info.RetryIndex,
		// Dynamic rules can depend on private request content. That input is
		// deliberately not persisted; this is NOT a replayable pricing contract.
		"automatic_charge_allowed": false, "pricing_replay_supported": false,
	}
	if info.ChannelMeta != nil {
		snapshot["upstream_model"] = info.UpstreamModelName
		snapshot["channel_type"] = info.ChannelType
		snapshot["channel_key_index"] = info.ChannelMultiKeyIndex
		if info.ApiKey != "" {
			item.CredentialFingerprint = fmt.Sprintf("%x", sha256.Sum256([]byte(info.ApiKey)))
		}
	}
	data, err := common.Marshal(snapshot)
	if err != nil {
		return err
	}
	item.RequestSnapshot = string(data)
	return model.CreateCancellationObservation(item)
}

var cancellationObservationOnce sync.Once

func StartCancellationObservationTask() {
	if !common.IsMasterNode {
		return
	}
	cancellationObservationOnce.Do(func() {
		go func() {
			ticker := time.NewTicker(time.Minute)
			defer ticker.Stop()
			for {
				if err := RunCancellationObservationOnce(common.GetTimestamp()); err != nil {
					common.SysLog("cancellation observation failed: " + err.Error())
				}
				<-ticker.C
			}
		}()
	})
}

// RunCancellationObservationOnce never calls upstreams or billing/refund APIs.
// Supplier adapters must be implemented and validated independently before
// any authoritative supplier evidence or hypothetical charge can be reported.
func RunCancellationObservationOnce(now int64) error {
	if err := backfillCancellationObservations(now); err != nil {
		return err
	}
	items, err := model.ListDueCancellationObservations(now, 200)
	if err != nil {
		return err
	}
	for _, item := range items {
		if err := refreshCancellationObservation(item, now); err != nil {
			return err
		}
	}
	return nil
}

func backfillCancellationObservations(now int64) error {
	cursor, err := model.LoadCancellationObservationCursor(now)
	if err != nil {
		return err
	}
	var logs []model.Log
	err = model.LOG_DB.Select("id", "request_id", "user_id", "token_id", "channel_id", "model_name", "created_at", "is_stream", "other").
		Where("id > ? AND created_at >= ? AND created_at <= ? AND type = ?", cursor.AfterId, cursor.Since, now-60, model.LogTypeError).
		Order("id ASC").Limit(500).Find(&logs).Error
	if err != nil {
		return err
	}
	for _, row := range logs {
		var other struct {
			ErrorCode          string `json:"error_code"`
			DurationMS         int64  `json:"duration_ms"`
			ReceivedResponses  int    `json:"received_response_count"`
			UpstreamResponseID string `json:"upstream_response_id"`
		}
		if common.UnmarshalJsonStr(row.Other, &other) != nil || other.ErrorCode != "client_canceled" || row.UserId <= 0 || row.ChannelId <= 0 || row.RequestId == "" {
			continue
		}
		item := &model.CancellationObservation{
			RequestId: row.RequestId, UserId: row.UserId, TokenId: row.TokenId, ChannelId: row.ChannelId,
			ModelName: row.ModelName, Source: "audit_backfill", CreatedAt: row.CreatedAt,
			DurationMS: other.DurationMS, IsStream: row.IsStream, ReceivedResponses: other.ReceivedResponses,
			UpstreamResponseId: other.UpstreamResponseID,
			RequestSnapshot:    `{"version":1,"mode":"observe_only","historical_snapshot_missing":true,"automatic_charge_allowed":false,"pricing_replay_supported":false}`,
		}
		if err := model.CreateCancellationObservation(item); err != nil {
			return err // Retain the cursor and retry; never silently lose a case.
		}
	}
	if len(logs) == 0 {
		return model.AdvanceCancellationObservationCursor(*cursor, 0, now-86400)
	}
	return model.AdvanceCancellationObservationCursor(*cursor, logs[len(logs)-1].Id, cursor.Since)
}

func refreshCancellationObservation(item model.CancellationObservation, now int64) error {
	var logs []model.Log
	err := model.LOG_DB.Select("id", "user_id", "token_id", "type", "quota", "accounting_status").
		Where("request_id = ? AND user_id > 0 AND type IN ?", item.RequestId, []int{model.LogTypeConsume, model.LogTypeRefund}).Find(&logs).Error
	if err != nil {
		return err
	}
	consumes, refunds := []int{}, []int{}
	identityConflict := false
	for _, row := range logs {
		if row.UserId != item.UserId || row.TokenId != item.TokenId {
			identityConflict = true
			continue
		}
		if row.Type == model.LogTypeConsume {
			consumes = append(consumes, row.Id)
		} else {
			refunds = append(refunds, row.Id)
		}
	}
	var holds []model.BillingHold
	if err := model.DB.Where("request_id = ?", item.RequestId).Find(&holds).Error; err != nil {
		return err
	}
	holdStatus := ""
	for _, hold := range holds {
		if hold.UserId != item.UserId || hold.TokenId != item.TokenId {
			identityConflict = true
		} else {
			holdStatus = hold.Status
		}
	}
	state, reason := "no_settlement_observed", "supplier_evidence_not_collected"
	if item.CredentialFingerprint == "" || (item.UpstreamRequestId == "" && item.UpstreamResponseId == "") {
		reason = "exact_supplier_link_missing"
	}
	switch {
	case identityConflict:
		state, reason = "manual_review", "identity_conflict"
	case len(consumes) > 1 || (len(consumes) > 0 && (len(refunds) > 0 || holdStatus != "")):
		state, reason = "manual_review", "multiple_billing_records"
	case len(consumes) == 1:
		state, reason = "consume_log_observed", "existing_consume_do_not_charge"
	case len(refunds) > 0 || holdStatus == model.BillingHoldStatusRefunded:
		state, reason = "refund_observed", "refund_do_not_charge"
	case holdStatus != "":
		state, reason = "hold_observed", "existing_hold_do_not_charge"
	}
	evidence, err := common.Marshal(map[string]interface{}{
		"mode": "observe_only", "consume_log_ids": consumes, "refund_log_ids": refunds,
		"billing_hold_status": holdStatus, "supplier_usage": nil, "supplier_cost": nil,
		"proposed_customer_charge": nil, "automatic_charge_allowed": false,
	})
	if err != nil {
		return err
	}
	return model.UpdateCancellationObservation(item, state, string(evidence), reason, now)
}
