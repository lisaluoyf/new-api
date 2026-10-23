package model

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestVideoFeeTerminalFailureRefundEvidenceRequiresOriginalReleasedHold(t *testing.T) {
	row := lookupTestOriginal(t, false)
	row.RequestID = "req-original-terminal"
	require.NoError(t, DB.Model(&row).Update("request_id", row.RequestID).Error)
	hold := &BillingHold{RequestId: row.RequestID, UserId: row.UserID, ChannelId: 273, PreConsumedQuota: 40}
	o := lookupTestObservation()
	o.Key = "apimaster-" + row.TaskID
	o.Task.Status = "failed"
	o.Status = "failed"
	o.Billing = VideoFeeBillingFacts{Status: "recorded", Currency: "CNY", Reservation: "released", Hold: VideoFeeLedgerFacts{Status: "recorded", Entries: []VideoFeeLedgerEntry{{ID: "hold-original", Type: "hold", Credit: "194400", Yuan: "19.44"}}}, Release: VideoFeeLedgerFacts{Status: "recorded", Entries: []VideoFeeLedgerEntry{{ID: "release-original", Type: "release", Credit: "194400", Yuan: "19.44"}}}, Charge: VideoFeeLedgerFacts{Status: "unknown"}, Refund: VideoFeeLedgerFacts{Status: "unknown"}}
	save := func() {
		raw, e := common.Marshal(o)
		require.NoError(t, e)
		require.NoError(t, DB.Model(&row).Update("provider_observation", string(raw)).Error)
	}
	save()
	require.True(t, VideoFeeHoldRefundEvidence(hold))
	// An unknown external bill stays unknown; this is solely customer refund evidence.
	require.Equal(t, "unknown", o.Billing.Charge.Status)
	o.Billing.Release.Entries[0].Yuan = "10"
	save()
	require.False(t, VideoFeeHoldRefundEvidence(hold))
	o.Billing.Release.Entries[0].Yuan = "19.44"
	o.Task.Status = "processing"
	o.Status = "processing"
	save()
	require.False(t, VideoFeeHoldRefundEvidence(hold))
	o.Task.Status = "failed"
	o.Status = "failed"
	o.Billing.Charge.Entries = []VideoFeeLedgerEntry{{ID: "capture", Type: "capture", Credit: "1", Yuan: "0.0001"}}
	o.Billing.Charge.Status = "recorded"
	save()
	require.False(t, VideoFeeHoldRefundEvidence(hold))
	o.Billing.Charge = VideoFeeLedgerFacts{Status: "unknown"}
	o.ObservedAt = time.Now().Add(-25 * time.Hour).UTC().Format(time.RFC3339Nano)
	save()
	require.False(t, VideoFeeHoldRefundEvidence(hold))
	o.ObservedAt = time.Now().UTC().Format(time.RFC3339Nano)
	o.Key = "other-original"
	save()
	require.False(t, VideoFeeHoldRefundEvidence(hold))
	o.Key = "apimaster-" + row.TaskID
	save()
	hold.PreConsumedQuota++
	require.False(t, VideoFeeHoldRefundEvidence(hold))
}
