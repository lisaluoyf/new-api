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
	unresolved := row.Status == model.VideoSubmissionUnknown || row.Status == model.VideoSubmissionSubmitting || (row.Status == model.VideoSubmissionAccepted && !row.TaskReady)
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
	zero := videoSubmissionFinalGenerationZero(row)
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
		"original_generation_billing_final": zero,
		"original_generation_zero_net":      zero,
		"safe_new_attempt":                  row.Status == model.VideoSubmissionNotAccepted && zero,
		"independent_asset_fees_status":     "unverified",
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

// Only a validated durable provider fence AND completed customer refund prove
// zero for the original generation. No empty/unknown ledger is treated as zero;
// independent material certification fees remain outside this proof.
func videoSubmissionFinalGenerationZero(row *model.VideoSubmission) bool {
	if row.Status != model.VideoSubmissionNotAccepted || row.TaskReady || row.UpstreamTaskID != "" || row.ChannelID != 273 || !row.ProviderLookupEnabled || !row.ProviderLookupDone || !row.ProviderRefundCompleted {
		return false
	}
	var o model.VideoFeeSubmissionObservation
	if common.UnmarshalJsonStr(string(row.ProviderObservation), &o) != nil || o.Acceptance != "not_accepted" {
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
