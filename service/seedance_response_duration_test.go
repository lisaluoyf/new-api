package service

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestSeedanceReceiptPreservesReportedStringDuration(t *testing.T) {
	for _, raw := range []string{`10.5`, `"10.5"`} {
		task := &model.Task{Data: []byte(`{"duration":` + raw + `}`), PrivateData: model.TaskPrivateData{SeedanceBillingReceiptEnabled: true, SeedanceRequest: map[string]any{"duration": 30}}}
		out := seedanceOutputDuration(task, nil)
		require.NotNil(t, out.ActualSeconds)
		require.Equal(t, 10.5, *out.ActualSeconds)
		require.Equal(t, 11, out.BillableSeconds)
		require.Equal(t, "provider_reported", out.Source)
	}
}

func TestSeedanceStringDurationSettlesExistingReceiptWithoutSecondDebit(t *testing.T) {
	truncate(t)
	const uid, tid, cid = 9177, 9177, 9177
	seedUser(t, uid, 810000)
	seedToken(t, tid, uid, "duration-recovery-test", 810000)
	seedChannel(t, cid)
	task := makeTask(uid, cid, 157500, tid, BillingSourceWallet, 0)
	task.ID = 0
	task.PrivateData.UpstreamTaskID = "original-provider-task"
	task.Properties.OriginModelName = "seedance-2.5"
	task.PrivateData.SeedanceRequest = map[string]any{"duration": 10}
	task.PrivateData.BillingContext = &model.TaskBillingContext{SeedanceTariff: true, OriginModelName: "seedance-2.5", ModelPrice: .1, GroupRatio: 1.05, OtherRatios: map[string]float64{"seconds": 10, "size": .3}}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set("seedance_billing_snapshot", map[string]any{"duration": 10, "billing_variant": "720P"})
	price := types.PriceData{ModelPrice: .1, GroupRatioInfo: types.GroupRatioInfo{GroupRatio: 1.05}, OtherRatios: map[string]float64{"seconds": 10, "size": .3}}
	receipt, err := NewSeedanceTaskReceipt(c, &relaycommon.RelayInfo{OriginModelName: "seedance-2.5", PriceData: price}, task)
	require.NoError(t, err)
	require.NoError(t, model.InsertSeedanceTaskWithReceipt(task, receipt))
	originalID := task.TaskID
	task.Status = model.TaskStatusSuccess
	task.Data = []byte(`{"duration":"10","content":{"video_url":"https://example.com/original.mp4"}}`)
	for i := 0; i < 3; i++ {
		SettleTaskBillingOnComplete(context.Background(), tariffPanicAdaptor{}, task, &relaycommon.TaskInfo{BillableSeconds: 10})
	}
	require.Equal(t, originalID, task.TaskID)
	require.Equal(t, "original-provider-task", task.GetUpstreamTaskID())
	require.Equal(t, 810000, getUserQuota(t, uid))
	require.Equal(t, 810000, getTokenRemainQuota(t, tid))
	got, err := model.GetSeedanceBillingReceipt(uid, originalID)
	require.NoError(t, err)
	var details model.SeedanceBillingDetails
	require.NoError(t, common.Unmarshal(got.Details, &details))
	require.Equal(t, "settled", details.SettlementStatus)
	require.Equal(t, 10.0, *details.Output.ActualSeconds)
	require.Equal(t, 10, details.Output.BillableSeconds)
	require.Equal(t, "provider_reported", details.Output.Source)
	require.Equal(t, "0.000000", details.AdditionalDebitAmount)
}
