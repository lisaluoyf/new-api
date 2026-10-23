package service

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestVideoSubmissionRecoveryUnknownNeverPermitsReplacement(t *testing.T) {
	now := time.Now().Unix()
	row := model.VideoSubmission{ID: "public-reference", TaskID: "task_original", Status: model.VideoSubmissionUnknown, CreatedAt: now - 3600, ErrorCode: "do_request_failed"}
	row.ProviderLookupEnabled = true
	row.ChannelID = 273
	row.ProviderRefundCompleted = true
	row.ProviderLookupAttempts = 57
	row.ProviderLookupNext = now + 300
	row.ProviderLookupLastCode = "provider_reconciliation_unavailable"
	row.ProviderLookupLastAt = now - 1
	d := VideoSubmissionRecoveryDTO(&row, now)
	require.Equal(t, true, d["manual_review_required"])
	require.Equal(t, "manual_review_required", d["escalation_status"])
	require.Equal(t, false, d["acceptance_final"])
	require.Equal(t, false, d["original_generation_billing_final"])
	require.Equal(t, false, d["safe_new_attempt"])
	require.Equal(t, true, d["temporary_technical_failure"])
	require.Equal(t, false, d["error_allows_new_submission"])
	require.Nil(t, d["resolution_deadline"])
	require.Equal(t, 57, d["lookup_attempts"])
	require.Equal(t, now+300, d["next_check_at"])
	// Younger rows stay queued; missing legacy timestamps cannot invent an SLA.
	row.CreatedAt = now - 60
	require.Equal(t, false, VideoSubmissionRecoveryDTO(&row, now)["manual_review_required"])
	row.CreatedAt = 0
	require.Equal(t, false, VideoSubmissionRecoveryDTO(&row, now)["manual_review_required"])
}

func TestVideoSubmissionRecoveryRetryPermissionDoesNotProveSupplierZero(t *testing.T) {
	row := model.VideoSubmission{TaskID: "task_original", ChannelID: 273, Status: model.VideoSubmissionNotAccepted}
	row.ProviderLookupEnabled = true
	row.ProviderLookupDone = true
	row.ProviderRefundCompleted = true
	facts := model.VideoFeeLedgerFacts{Status: "not_applicable"}
	o := model.VideoFeeSubmissionObservation{Object: "submission", Key: videoFeeSubmissionKeyPrefix + row.TaskID, Acceptance: "not_accepted", ObservedAt: time.Now().Add(-48 * time.Hour).UTC().Format(time.RFC3339Nano), NewSubmissionSafe: true, Billing: model.VideoFeeBillingFacts{Reservation: "none", Hold: facts, Release: facts, Charge: facts, Refund: facts}}
	save := func() {
		raw, err := common.Marshal(o)
		require.NoError(t, err)
		row.ProviderObservation = model.VideoFeeEvidenceJSON(raw)
	}
	save()
	d := VideoSubmissionRecoveryDTO(&row, time.Now().Unix())
	require.Equal(t, true, d["safe_new_attempt"])
	require.Equal(t, false, d["original_generation_zero_net"])
	require.Equal(t, false, d["original_generation_billing_final"])
	require.Equal(t, true, d["customer_refund_completed"])
	require.Equal(t, "free_per_provider_policy", d["independent_asset_fees_status"])
	require.Equal(t, false, d["cancellation_supported"])
	require.Nil(t, d["next_check_at"])
	o.NewSubmissionSafe = false
	save()
	require.Equal(t, false, VideoSubmissionRecoveryDTO(&row, time.Now().Unix())["safe_new_attempt"])
	o.NewSubmissionSafe = true
	save()
	row.ProviderRefundCompleted = false
	require.Equal(t, false, VideoSubmissionRecoveryDTO(&row, time.Now().Unix())["safe_new_attempt"])
	row.ProviderRefundCompleted = true
	o.Billing.Charge.Status = "unknown"
	save()
	require.Equal(t, false, VideoSubmissionRecoveryDTO(&row, time.Now().Unix())["safe_new_attempt"])
	o.Billing.Charge = facts
	o.Key = "wrong-original"
	save()
	require.Equal(t, false, VideoSubmissionRecoveryDTO(&row, time.Now().Unix())["safe_new_attempt"])
}
