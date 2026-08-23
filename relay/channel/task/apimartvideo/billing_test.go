package apimartvideo

import (
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
