package apimartvideo

import (
	"context"
	"fmt"
	"math"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/stretchr/testify/require"
)

func TestSeedanceActualQuotaUsesUpstreamCost(t *testing.T) {
	task := &model.Task{
		Data: []byte(`{"data":{"cost":1.4176}}`),
		PrivateData: model.TaskPrivateData{BillingContext: &model.TaskBillingContext{
			OriginModelName: ModelDoubaoSeedance20,
			GroupRatio:      1.05,
		}},
	}
	got := (&TaskAdaptor{}).AdjustBillingOnComplete(task, &relaycommon.TaskInfo{})
	require.Equal(t, int(math.Round(1.4176*common.QuotaPerUnit*1.05)), got)
}

func TestSeedanceActualQuotaFallsBackToCredits(t *testing.T) {
	task := &model.Task{
		Data: []byte(`{"data":{"credits_cost":5.68}}`),
		PrivateData: model.TaskPrivateData{BillingContext: &model.TaskBillingContext{
			OriginModelName: ModelDoubaoSeedance20,
			GroupRatio:      1,
		}},
	}
	got := (&TaskAdaptor{}).AdjustBillingOnComplete(task, &relaycommon.TaskInfo{})
	require.Equal(t, int(math.Round(0.568*common.QuotaPerUnit)), got)
}

func TestRenamedSeedancePreservesMappingAndSettlement(t *testing.T) {
	for _, name := range []string{ModelSeedance20, ModelDoubaoSeedance20} {
		require.True(t, IsVideoModel(name))
		payload := openAIToApimart(relaycommon.TaskSubmitReq{Model: name, Duration: 5}, ModelDoubaoSeedance20)
		require.Equal(t, ModelDoubaoSeedance20, payload.Model)
		task := &model.Task{
			Data:        []byte(`{"data":{"cost":1.4176}}`),
			Properties:  model.Properties{OriginModelName: name},
			PrivateData: model.TaskPrivateData{BillingContext: &model.TaskBillingContext{GroupRatio: 1.05}},
		}
		require.Equal(t, int(math.Round(1.4176*common.QuotaPerUnit*1.05)), (&TaskAdaptor{}).AdjustBillingOnComplete(task, &relaycommon.TaskInfo{}))
	}
}

func TestRecalcMotionControlQuotaAdjustsSeconds(t *testing.T) {
	task := &model.Task{
		Quota: 30000,
		PrivateData: model.TaskPrivateData{
			BillingContext: &model.TaskBillingContext{
				OtherRatios: map[string]float64{
					"seconds": 3,
					"mode":    1,
				},
			},
		},
	}
	got := recalcMotionControlQuota(task, 4)
	require.Equal(t, 40000, got)
}

func TestRecalcMotionControlQuotaNoChange(t *testing.T) {
	task := &model.Task{
		Quota: 30000,
		PrivateData: model.TaskPrivateData{
			BillingContext: &model.TaskBillingContext{
				OtherRatios: map[string]float64{
					"seconds": 3,
					"mode":    1,
				},
			},
		},
	}
	require.Equal(t, 0, recalcMotionControlQuota(task, 3))
}

func TestExtractBillableSecondsFromApimart(t *testing.T) {
	body := []byte(`{"data":{"duration":4.2,"status":"completed"}}`)
	require.Equal(t, 5, extractBillableSecondsFromApimart(body))

	costBody := []byte(`{"data":{"cost":0.41152,"status":"completed"}}`)
	require.Zero(t, extractBillableSecondsFromApimart(costBody))
	require.Equal(t, 4, extractBillableSecondsFromApimartWithMode(costBody, "std"))
}

// A 29s reference can produce a 3s output. Output length must never refund
// reference processing, whether explicit provider billing is present or absent.
func TestMotionControlSettlementIgnoresShortOutput(t *testing.T) {
	for _, mode := range []string{"std", "pro"} {
		rate := StdUSDPerSecond
		if mode == "pro" {
			rate = ProUSDPerSecond
		}
		for _, group := range []float64{1, 1.05} {
			for _, withCost := range []bool{true, false} {
				t.Run(fmt.Sprintf("%s/group_%g/cost_%t", mode, group, withCost), func(t *testing.T) {
					cost := 0.0
					if withCost {
						cost = 29 * rate
					}
					task := &model.Task{
						Quota: int(math.Round(8 * rate * common.QuotaPerUnit * group)),
						Data:  []byte(fmt.Sprintf(`{"data":{"duration":3,"output_duration":3,"actual_duration":3,"result":{"duration":3},"cost":%.8f}}`, cost)),
						PrivateData: model.TaskPrivateData{
							RequestData: fmt.Sprintf(`{"video_url":"https://reference.example/29s.mp4","mode":%q}`, mode),
							BillingContext: &model.TaskBillingContext{
								OriginModelName: ModelKlingV3MotionControl,
								GroupRatio:      group,
								OtherRatios:     map[string]float64{"seconds": 8, "mode": modeBillingRatio(mode)},
							},
						},
					}
					probes := 0
					got := adjustMotionControlQuota(task, func(_ context.Context, url string) (int, error) {
						probes++
						require.Equal(t, "https://reference.example/29s.mp4", url)
						return 29, nil
					})
					// Existing quota rounding is preserved during reconciliation.
					require.Equal(t, int(math.Round(float64(task.Quota)/8*29)), got)
					if withCost {
						require.Zero(t, probes)
					} else {
						require.Equal(t, 1, probes)
					}
				})
			}
		}
	}
}

func TestMotionControlExplicitBillingBeatsOutputDuration(t *testing.T) {
	for _, field := range []string{"billable_seconds", "billable_duration"} {
		body := []byte(fmt.Sprintf(`{"data":{"%s":29,"duration":3,"output_duration":3}}`, field))
		require.Equal(t, 29, extractBillableSecondsFromApimartWithMode(body, "std"))
	}
	require.Zero(t, extractBillableSecondsFromApimartWithMode([]byte(`{"data":{"duration":3,"actual_duration":3,"output_duration":3,"result":{"duration":3}}}`), "std"))
}

func TestMotionControlUnavailableReferenceKeepsPreDeduction(t *testing.T) {
	task := &model.Task{
		Quota: 1566348,
		Data:  []byte(`{"data":{"duration":3,"output_duration":3}}`),
		PrivateData: model.TaskPrivateData{
			RequestData:    `{"video_url":"https://reference.example/29s.mp4"}`,
			BillingContext: &model.TaskBillingContext{OriginModelName: ModelKlingV3MotionControl, OtherRatios: map[string]float64{"seconds": 29}},
		},
	}
	require.Zero(t, adjustMotionControlQuota(task, func(_ context.Context, url string) (int, error) {
		require.Equal(t, "https://reference.example/29s.mp4", url)
		return 0, fmt.Errorf("reference unavailable")
	}))
	// No reference URL: even a parsed result's generic BillableSeconds is unsafe.
	task.PrivateData.RequestData = ""
	require.Zero(t, (&TaskAdaptor{}).AdjustBillingOnComplete(task, &relaycommon.TaskInfo{BillableSeconds: 3, Url: "https://output.example/3s.mp4"}))
}
