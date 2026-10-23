package service

import (
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

const VideoSubmissionReviewAfterSeconds int64 = 30 * 60

// A projection of persisted facts only. Review eligibility is not a ticket,
// acceptance deadline, cancellation or permission to send a generation.
func VideoSubmissionRecoveryDTO(row *model.VideoSubmission, now int64) map[string]any {
	financialPending := row.ChannelID == 273 && row.ProviderLookupEnabled && !row.ProviderLookupDone && row.ProviderObservation != "" && !row.ProviderRefundCompleted && !row.RecoverySettled
	unresolved := financialPending || row.Status == model.VideoSubmissionUnknown || row.Status == model.VideoSubmissionSubmitting || (row.Status == model.VideoSubmissionAccepted && !row.TaskReady)
	reviewAt := row.CreatedAt + VideoSubmissionReviewAfterSeconds
	review := unresolved && row.CreatedAt > 0 && now >= reviewAt
	state := "not_required"
	if unresolved {
		state = "waiting"
	}
	if review {
		state = "manual_review_required"
	}
	finalAcceptance := row.Status == model.VideoSubmissionAccepted || row.Status == model.VideoSubmissionNotAccepted
	retryAllowed := videoSubmissionProviderRetryAllowed(row)
	assetFees := "unverified"
	if row.ChannelID == 273 {
		assetFees = "free_per_provider_policy"
	}
	var next any
	if row.ProviderLookupEnabled && row.ChannelID == 273 && !row.ProviderLookupDone && row.ProviderLookupNext > 0 {
		next = row.ProviderLookupNext
	}
	var last any
	if row.ProviderLookupLastAt > 0 {
		last = row.ProviderLookupLastAt
	}
	code := row.ProviderLookupLastCode
	if code != "provider_observation_recorded" && code != "provider_reconciliation_unavailable" {
		code = ""
	}
	return map[string]any{
		"acceptance_final":                  finalAcceptance,
		"original_generation_billing_final": false,
		"original_generation_zero_net":      false,
		"safe_new_attempt":                  row.Status == model.VideoSubmissionNotAccepted && retryAllowed,
		"independent_asset_fees_status":     assetFees,
		"customer_refund_completed":         row.ProviderRefundCompleted,
		"supplier_billing_final":            false,
		"cancellation_supported":            false,
		"manual_review_required":            review,
		"escalation_status":                 state,
		"review_after":                      reviewAt,
		"support_reference":                 row.ID,
		"external_ticket_status":            "not_tracked",
		"resolution_deadline":               nil,
		"lookup_attempts":                   row.ProviderLookupAttempts,
		"last_lookup_at":                    last,
		"last_lookup_code":                  code,
		"next_check_at":                     next,
		"next_check_is_resolution_deadline": false,
		"temporary_technical_failure":       videoSubmissionTemporaryError(row.ErrorCode),
		"error_allows_new_submission":       false,
	}
}

// Retry permission is a provider business rule plus completed customer refund.
// It is neither a durable execution fence nor proof of the external supplier bill.
func videoSubmissionProviderRetryAllowed(row *model.VideoSubmission) bool {
	if row.Status != model.VideoSubmissionNotAccepted || row.TaskReady || row.UpstreamTaskID != "" || row.ChannelID != 273 || !row.ProviderLookupEnabled || !row.ProviderLookupDone || !row.ProviderRefundCompleted {
		return false
	}
	var o model.VideoFeeSubmissionObservation
	if common.UnmarshalJsonStr(string(row.ProviderObservation), &o) != nil || !o.RejectedWithRetryPermission() {
		return false
	}
	at, err := time.Parse(time.RFC3339Nano, o.ObservedAt)
	return err == nil && o.Validate(videoFeeSubmissionKeyPrefix+row.TaskID, at) == nil
}

func videoSubmissionTemporaryError(code string) bool {
	switch code {
	case "do_request_failed", "read_response_body_failed", "submission_storage_unavailable", "refund_receipt_unavailable", "build_request_failed":
		return true
	}
	return false
}
