package service

import (
	"context"
	"crypto/sha256"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"github.com/tidwall/gjson"
)

func seedanceProtectedMediaID(userID int, url string) string {
	hash := sha256.Sum256([]byte(fmt.Sprintf("%d:%s", userID, url)))
	return fmt.Sprintf("media_%x", hash[:])
}

func seedanceMoney(quota int) string {
	micros := int64(quota) * 2
	return fmt.Sprintf("%d.%06d", micros/1000000, micros%1000000)
}

func NewSeedanceBillingDetails(c *gin.Context, info *relaycommon.RelayInfo) (model.SeedanceBillingDetails, error) {
	v, exists := c.Get("seedance_billing_snapshot")
	if !exists {
		return model.SeedanceBillingDetails{}, fmt.Errorf("Seedance billing snapshot is missing")
	}
	snapshot := v.(map[string]any)
	variant := strings.ToUpper(fmt.Sprint(snapshot["billing_variant"]))
	variant = strings.ReplaceAll(variant, "-INPUT", "-input")
	baseRate, ok := snapshot["tariff_unit_rate"].(float64)
	if !ok {
		baseRate, ok = ratio_setting.GetVideoModelPrice(info.OriginModelName, variant)
	}
	if !ok || baseRate <= 0 {
		return model.SeedanceBillingDetails{}, fmt.Errorf("Seedance tariff is unavailable")
	}
	effectiveBeforeAccount := info.PriceData.ModelPrice * info.PriceData.OtherRatios["size"]
	accountMultiplier := info.PriceData.GroupRatioInfo.GroupRatio
	rate := seedanceEffectiveRate(effectiveBeforeAccount * accountMultiplier)
	encoded, _ := common.Marshal([]any{"seedance-seconds-v1", info.OriginModelName, variant, baseRate})
	hash := sha256.Sum256(encoded)
	input := seedanceInt(snapshot["video_input_seconds"])
	estimatedOutput := seedanceInt(snapshot["duration"])
	requested := estimatedOutput
	if snapshot["auto_duration"] == true {
		requested = -1
	}
	measurements := []model.SeedanceInputVideoMeasurement{}
	if m, ok := c.Get("seedance_video_input_measurements"); ok {
		measurements = m.([]model.SeedanceInputVideoMeasurement)
	}
	if len(measurements) == 0 && snapshot["input_video_measurements"] != nil {
		raw, _ := common.Marshal(snapshot["input_video_measurements"])
		_ = common.Unmarshal(raw, &measurements)
	}
	return model.SeedanceBillingDetails{
		SchemaVersion: "1", SettlementStatus: "estimate", Model: info.OriginModelName,
		Resolution: strings.TrimSuffix(variant, "-input"), TariffVariant: variant, TariffRevision: fmt.Sprintf("sd-v1-%x", hash[:8]),
		Currency: "USD", Unit: "second", BaseUnitRate: strconv.FormatFloat(baseRate, 'f', -1, 64),
		ChannelMultiplier: math.Round(effectiveBeforeAccount/baseRate*1e12) / 1e12, AccountMultiplier: accountMultiplier,
		EffectiveUnitRate: rate.String(), RequestedOutputSeconds: requested,
		EstimatedOutputSeconds: estimatedOutput, EstimatedBillableSeconds: estimatedOutput + input,
		InputVideos: measurements, BillableInputVideoSeconds: input,
		Rounding:        map[string]string{"input_video": "ceil_each_file_then_sum", "output_video": "nearest_integer_half_up", "money": "nearest_account_unit_half_up", "effective_unit_rate": "nearest_decimal_12_places_half_up", "amount_formula": "round(total_billable_seconds * effective_unit_rate / account_unit_usd) * account_unit_usd"},
		EstimatedAmount: seedanceMoney(SeedanceSubmissionQuota(info.PriceData)), ReservedAmount: "0.000000",
		AdditionalDebitAmount: "0.000000", RefundedAmount: "0.000000", AccountUnitUSD: "0.000002",
		MediaCharges:                map[string]string{"image": "0.000000", "audio": "0.000000", "other_reference": "0.000000", "input_video_unrounded": rate.Mul(decimal.NewFromInt(int64(input))).String(), "output_video_unrounded": rate.Mul(decimal.NewFromInt(int64(estimatedOutput))).String()},
		FinalDebitMayExceedEstimate: true, CreatedAt: time.Now().Unix(),
	}, nil
}

func NewSeedanceTaskReceipt(c *gin.Context, info *relaycommon.RelayInfo, task *model.Task) (*model.SeedanceBillingReceipt, error) {
	details, err := NewSeedanceBillingDetails(c, info)
	if err != nil {
		return nil, err
	}
	details.ReceiptID = "bill_" + task.TaskID
	details.TaskID = task.TaskID
	details.SettlementStatus = "pending"
	details.InitialQuotaUnits = task.Quota
	details.ReservedAmount = seedanceMoney(task.Quota)
	raw, err := common.Marshal(details)
	if err != nil {
		return nil, err
	}
	task.PrivateData.SeedanceBillingReceiptEnabled = true
	return &model.SeedanceBillingReceipt{ID: details.ReceiptID, TaskID: task.TaskID, UserID: task.UserId, Status: "pending", InitialQuota: task.Quota, Details: raw, CreatedAt: details.CreatedAt, UpdatedAt: details.CreatedAt}, nil
}

func CompleteSeedanceBillingReceipt(task *model.Task, refunded bool, output *model.SeedanceOutputDuration) {
	if !task.PrivateData.SeedanceBillingReceiptEnabled {
		return
	}
	receipt, err := model.GetSeedanceBillingReceipt(task.UserId, task.TaskID)
	if err != nil {
		logger.LogError(nil, fmt.Sprintf("read Seedance billing receipt %s: %v", task.TaskID, err))
		return
	}
	if receipt.Status != "pending" {
		return
	}
	var details model.SeedanceBillingDetails
	if err = common.Unmarshal(receipt.Details, &details); err != nil {
		return
	}
	now := time.Now().Unix()
	details.SettledAt = &now
	finalQuota := task.Quota
	if refunded {
		finalQuota = 0
		receipt.Status = "refunded"
	} else {
		receipt.Status = "settled"
	}
	receipt.FinalQuota = finalQuota
	details.FinalQuotaUnits = &finalQuota
	amount := seedanceMoney(finalQuota)
	details.FinalDebitAmount = &amount
	details.NetAmount = &amount
	details.SettlementStatus = receipt.Status
	if finalQuota > receipt.InitialQuota {
		details.AdditionalDebitAmount = seedanceMoney(finalQuota - receipt.InitialQuota)
	}
	if finalQuota < receipt.InitialQuota {
		details.RefundedAmount = seedanceMoney(receipt.InitialQuota - finalQuota)
	}
	if refunded {
		details.MediaCharges = map[string]string{"image": "0.000000", "audio": "0.000000", "other_reference": "0.000000", "input_video_unrounded": "0", "output_video_unrounded": "0"}
	}
	if !refunded && output != nil {
		details.Output = output
		total := output.BillableSeconds + details.BillableInputVideoSeconds
		details.BillableSeconds = &total
		if rate, err := decimal.NewFromString(details.EffectiveUnitRate); err == nil {
			details.MediaCharges["output_video_unrounded"] = rate.Mul(decimal.NewFromInt(int64(output.BillableSeconds))).String()
		}
	}
	if err = model.SaveSeedanceBillingReceipt(receipt, details); err != nil {
		logger.LogError(nil, fmt.Sprintf("save Seedance billing receipt %s: %v", task.TaskID, err))
	}
}

func AddSeedanceBillingReceipt(task *model.Task, out map[string]any) {
	if task == nil || !task.PrivateData.SeedanceBillingReceiptEnabled {
		return
	}
	receipt, err := model.GetSeedanceBillingReceipt(task.UserId, task.TaskID)
	if err != nil {
		return
	}
	var details model.SeedanceBillingDetails
	if common.Unmarshal(receipt.Details, &details) == nil {
		out["billing"] = details
	}
}

// Quota rounding remains authoritative; this helper also labels fallbacks so
// a requested duration can never be misrepresented as an actual measurement.
func seedanceOutputDuration(task *model.Task, result *relaycommon.TaskInfo) model.SeedanceOutputDuration {
	if task.PrivateData.SeedanceOutput != nil {
		return *task.PrivateData.SeedanceOutput
	}
	output := model.SeedanceOutputDuration{}
	for _, path := range seedanceOutputDurationPaths {
		if v := seedanceDurationValue(task.Data, path); v > 0 {
			output.ActualSeconds = &v
			output.BillableSeconds = int(math.Round(v))
			output.Source = "provider_reported"
			break
		}
	}
	if output.BillableSeconds <= 0 && task.PrivateData.SeedanceBillingReceiptEnabled {
		if u := seedanceOutputProbeURL(task, result); u != "" {
			if measured, err := ProbeRemoteVideoDuration(context.Background(), u); err == nil {
				output.ActualSeconds = &measured
				output.BillableSeconds = int(math.Round(measured))
				output.Source = "apimaster_probe"
			}
		}
	}
	if output.BillableSeconds <= 0 && result != nil && result.BillableSeconds > 0 {
		output.BillableSeconds = result.BillableSeconds
		output.Source = "provider_billable_seconds"
	}
	if output.BillableSeconds <= 0 && !task.PrivateData.SeedanceBillingReceiptEnabled && task.PrivateData.SeedanceRequest["auto_duration"] == true {
		if u := seedanceOutputProbeURL(task, result); u != "" {
			output.BillableSeconds, _ = ProbeRemoteVideoDurationSecondsRound(context.Background(), u)
			output.Source = "apimaster_probe"
		}
	}
	if output.BillableSeconds <= 0 {
		output.BillableSeconds = seedanceInt(task.PrivateData.SeedanceRequest["duration"])
		output.Source = "requested_duration_fallback"
	}
	if output.BillableSeconds <= 0 {
		output.BillableSeconds = int(math.Round(task.PrivateData.BillingContext.OtherRatios["seconds"])) - seedanceInt(task.PrivateData.SeedanceRequest["video_input_seconds"])
		output.Source = "reservation_fallback"
	}
	if task.PrivateData.SeedanceBillingReceiptEnabled {
		task.PrivateData.SeedanceOutput = &output
	}
	return output
}

var seedanceOutputDurationPaths = []string{"data.output_duration", "output_duration", "data.duration", "duration", "data.result.videos.0.duration", "result.videos.0.duration", "data.actual_duration", "data.result.duration"}

func seedanceDurationValue(data []byte, path string) float64 {
	v := gjson.GetBytes(data, path)
	if v.Type == gjson.Number {
		return v.Float()
	}
	return 0
}

func seedanceOutputProbeURL(task *model.Task, result *relaycommon.TaskInfo) string {
	if u := task.GetUpstreamVideoURL(); u != "" {
		return u
	}
	if result != nil && result.Url != "" {
		return result.Url
	}
	return task.GetResultURL()
}

// Repair receipt persistence after interruption without issuing any debit or
// refund. Finalize only when the recorded ledger agrees with task settlement.
func ReconcileSeedanceBillingReceipts(ctx context.Context) {
	receipts, err := model.PendingCompletedSeedanceReceipts(100)
	if err != nil {
		logger.LogError(ctx, fmt.Sprintf("scan pending Seedance receipts: %v", err))
		return
	}
	for _, receipt := range receipts {
		task, found, err := model.GetByTaskId(receipt.UserID, receipt.TaskID)
		if err != nil || !found || !task.PrivateData.SeedanceBillingReceiptEnabled {
			continue
		}
		net, logged, err := model.SeedanceTaskLedgerNet(task.UserId, task.TaskID)
		if err != nil || !logged {
			continue
		}
		if task.Status == model.TaskStatusFailure && net == 0 {
			CompleteSeedanceBillingReceipt(task, true, nil)
			continue
		}
		if task.Status == model.TaskStatusSuccess && net == task.Quota && net == SeedanceTariffQuota(task, nil) {
			output := seedanceOutputDuration(task, nil)
			CompleteSeedanceBillingReceipt(task, false, &output)
		}
	}
}
