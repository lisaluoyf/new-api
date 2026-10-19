package model

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
	"testing"
)

func prepareVideoFeeFinancialTests(t *testing.T) {
	require.NoError(t, DB.AutoMigrate(&BillingHold{}, &VideoSubmissionRefund{}))
}

func prepareVideoFeeRecoveryFixture(t *testing.T, snapshot *VideoSubmissionRecovery, row *VideoSubmission) {
	raw, err := common.Marshal(SeedanceBillingDetails{TaskID: row.TaskID, Model: row.Model})
	require.NoError(t, err)
	snapshot.Receipt = &SeedanceBillingReceipt{ID: "bill-original", TaskID: row.TaskID, UserID: row.UserID, InitialQuota: snapshot.Task.Quota, Details: raw}
}
