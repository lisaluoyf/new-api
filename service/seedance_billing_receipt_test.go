package service

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func TestSeedanceReceiptMatchesWalletSettlementAndSurvivesRepeatedReads(t *testing.T) {
	truncate(t)
	uid, tid, cid := 9101, 9101, 9101
	seedUser(t, uid, 811000)
	seedToken(t, tid, uid, "receipt-test", 811000)
	seedChannel(t, cid)
	task := makeTask(uid, cid, 189000, tid, BillingSourceWallet, 0)
	task.ID = 0
	task.PrivateData.BillingContext = &model.TaskBillingContext{SeedanceTariff: true, OriginModelName: "seedance-2.5", ModelPrice: .1, GroupRatio: 1.05, OtherRatios: map[string]float64{"seconds": 12, "size": .3}}
	task.Properties.OriginModelName = "seedance-2.5"
	task.PrivateData.SeedanceRequest = map[string]any{"duration": 5, "video_input_seconds": 7}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set("seedance_billing_snapshot", map[string]any{"duration": 5, "video_input_seconds": 7, "billing_variant": "720P-input"})
	raw1, raw2 := 3.1, 2.01
	c.Set("seedance_video_input_measurements", []model.SeedanceInputVideoMeasurement{{Index: 0, MediaID: "media_a", MeasuredSeconds: &raw1, BillableSeconds: 4, DurationSource: "apimaster_probe"}, {Index: 1, MediaID: "media_b", MeasuredSeconds: &raw2, BillableSeconds: 3, DurationSource: "apimaster_probe"}})
	price := types.PriceData{ModelPrice: .1, GroupRatioInfo: types.GroupRatioInfo{GroupRatio: 1.05}, OtherRatios: map[string]float64{"seconds": 12, "size": .3}}
	receipt, err := NewSeedanceTaskReceipt(c, &relaycommon.RelayInfo{OriginModelName: "seedance-2.5", PriceData: price}, task)
	require.NoError(t, err)
	require.NoError(t, model.InsertSeedanceTaskWithReceipt(task, receipt))
	task.Status = model.TaskStatusSuccess
	task.Data = []byte(`{"duration":6.5,"cost":9999}`)
	SettleTaskBillingOnComplete(context.Background(), tariffPanicAdaptor{}, task, &relaycommon.TaskInfo{TotalTokens: 999999})
	require.Equal(t, 220500, task.Quota)
	require.Equal(t, 779500, getUserQuota(t, uid))
	got, err := model.GetSeedanceBillingReceipt(uid, task.TaskID)
	require.NoError(t, err)
	var details model.SeedanceBillingDetails
	require.NoError(t, common.Unmarshal(got.Details, &details))
	require.Equal(t, "settled", details.SettlementStatus)
	require.Equal(t, "0.0315", details.EffectiveUnitRate)
	require.Equal(t, "0.378000", details.ReservedAmount)
	require.Equal(t, "0.063000", details.AdditionalDebitAmount)
	require.Equal(t, "0.441000", *details.NetAmount)
	require.Equal(t, 14, *details.BillableSeconds)
	require.Equal(t, 6.5, *details.Output.ActualSeconds)
	require.Equal(t, 7, details.Output.BillableSeconds)
	require.Equal(t, "provider_reported", details.Output.Source)
	rate, err := decimal.NewFromString(details.EffectiveUnitRate)
	require.NoError(t, err)
	require.Equal(t, *details.FinalQuotaUnits, seedanceQuotaFromRate(rate, *details.BillableSeconds))
	before := string(got.Details)
	for i := 0; i < 3; i++ {
		SettleTaskBillingOnComplete(context.Background(), tariffPanicAdaptor{}, task, &relaycommon.TaskInfo{})
		out := map[string]any{}
		AddSeedanceResultFields(task, out)
		require.Contains(t, out, "billing")
		again, err := model.GetSeedanceBillingReceipt(uid, task.TaskID)
		require.NoError(t, err)
		require.Equal(t, before, string(again.Details))
	}
	require.Equal(t, 779500, getUserQuota(t, uid))
	_, err = model.GetSeedanceBillingReceipt(uid+1, task.TaskID)
	require.Error(t, err)
}

func TestSeedanceReceiptFullRefundPersistsAndLegacyTasksHaveNoInventedReceipt(t *testing.T) {
	truncate(t)
	uid, tid, cid := 9102, 9102, 9102
	seedUser(t, uid, 900000)
	seedToken(t, tid, uid, "refund-receipt-test", 900000)
	seedChannel(t, cid)
	task := makeTask(uid, cid, 100000, tid, BillingSourceWallet, 0)
	task.ID = 0
	task.PrivateData.SeedanceBillingReceiptEnabled = true
	task.PrivateData.BillingContext = &model.TaskBillingContext{SeedanceTariff: true, OriginModelName: "seedance-2.0", ModelPrice: .1, GroupRatio: 1, OtherRatios: map[string]float64{"seconds": 2, "size": 1}}
	details := model.SeedanceBillingDetails{ReceiptID: "bill_" + task.TaskID, TaskID: task.TaskID, SettlementStatus: "pending", ReservedAmount: "0.200000", InputVideos: []model.SeedanceInputVideoMeasurement{}}
	raw, _ := common.Marshal(details)
	receipt := &model.SeedanceBillingReceipt{ID: details.ReceiptID, TaskID: task.TaskID, UserID: uid, InitialQuota: 100000, Status: "pending", Details: raw}
	require.NoError(t, model.InsertSeedanceTaskWithReceipt(task, receipt))
	task.Status = model.TaskStatusFailure
	RefundTaskQuota(context.Background(), task, "provider failed")
	got, err := model.GetSeedanceBillingReceipt(uid, task.TaskID)
	require.NoError(t, err)
	require.NoError(t, common.Unmarshal(got.Details, &details))
	require.Equal(t, "refunded", details.SettlementStatus)
	require.Equal(t, "0.200000", details.RefundedAmount)
	require.Equal(t, "0.000000", *details.NetAmount)
	require.Equal(t, 1000000, getUserQuota(t, uid))
	for i := 0; i < 3; i++ {
		out := map[string]any{}
		AddSeedanceBillingReceipt(task, out)
		require.Contains(t, out, "billing")
	}
	require.Equal(t, 1000000, getUserQuota(t, uid))
	task.PrivateData.SeedanceBillingReceiptEnabled = false
	out := map[string]any{}
	AddSeedanceBillingReceipt(task, out)
	require.NotContains(t, out, "billing")
}

func TestSeedanceInputMeasurementsIgnoreClientSecondsAndProtectURLs(t *testing.T) {
	db := seedanceTestDB(t)
	for _, a := range []model.SeedanceResource{{ID: "asset_raw1", Kind: "asset", UserID: 1, AssetType: "Video", Status: "Active", UpstreamID: "private1", DurationSeconds: 4, MeasuredDurationSeconds: 3.1}, {ID: "asset_raw2", Kind: "asset", UserID: 1, AssetType: "Video", Status: "Active", UpstreamID: "private2", DurationSeconds: 3, MeasuredDurationSeconds: 2.01}} {
		require.NoError(t, db.Create(&a).Error)
	}
	c := seedanceContext(1)
	fields := map[string]any{"model": "seedance-2.0-mini", "prompt": "scene", "input_seconds": 1000, "reference_seconds": 1000, "video_urls": []any{"asset://asset_raw1", "asset://asset_raw2"}}
	require.NoError(t, ValidateSeedanceVideoInputs(c, fields))
	require.Equal(t, 7, c.GetInt("seedance_video_input_seconds"))
	m, _ := c.Get("seedance_video_input_measurements")
	measured := m.([]model.SeedanceInputVideoMeasurement)
	require.Len(t, measured, 2)
	require.Equal(t, 3.1, *measured[0].MeasuredSeconds)
	require.Equal(t, 4, measured[0].BillableSeconds)
	require.Equal(t, 3, measured[1].BillableSeconds)
	raw, _ := common.Marshal(measured)
	require.NotContains(t, string(raw), "private")
	require.NotEqual(t, seedanceProtectedMediaID(1, "https://private.example/a"), seedanceProtectedMediaID(2, "https://private.example/a"))
	require.NotContains(t, seedanceProtectedMediaID(1, "https://private.example/a"), "private.example")
}

func TestSeedanceOutputFallbackIsExplicitAndDecimalRoundingIsReproducible(t *testing.T) {
	task := &model.Task{PrivateData: model.TaskPrivateData{SeedanceBillingReceiptEnabled: true, SeedanceRequest: map[string]any{"duration": 5}, BillingContext: &model.TaskBillingContext{OtherRatios: map[string]float64{"seconds": 5}}}}
	output := seedanceOutputDuration(task, nil)
	require.Nil(t, output.ActualSeconds)
	require.Equal(t, "requested_duration_fallback", output.Source)
	require.Equal(t, 5, output.BillableSeconds)
	require.Equal(t, 73815, seedanceQuotaFromRate(seedanceEffectiveRate(.03515*1.05), 4))
	require.Equal(t, 1, seedanceQuotaFromRate(decimal.RequireFromString("0.000001"), 1))
}

func TestSeedanceEstimateIncludesNoReservationOrPrivateReferences(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/videos/quote", strings.NewReader(`{}`))
	c.Set("seedance_billing_snapshot", map[string]any{"duration": 4, "video_input_seconds": 0, "billing_variant": "480P"})
	price := types.PriceData{ModelPrice: .0756, GroupRatioInfo: types.GroupRatioInfo{GroupRatio: 1.05}, OtherRatios: map[string]float64{"seconds": 4, "size": .03515 / .0756}}
	details, err := NewSeedanceBillingDetails(c, &relaycommon.RelayInfo{OriginModelName: "seedance-2.0-mini", PriceData: price})
	require.NoError(t, err)
	require.Equal(t, "0.147630", details.EstimatedAmount)
	require.Equal(t, "0.000000", details.ReservedAmount)
	require.Nil(t, details.FinalDebitAmount)
	require.Equal(t, "estimate", details.SettlementStatus)
	require.Empty(t, details.ReceiptID)
	require.Empty(t, details.InputVideos)
}
