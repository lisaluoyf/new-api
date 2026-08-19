package service

import (
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
)

func TestTaskActualChannelCostUSD(t *testing.T) {
	apikeyTask := &model.Task{Data: []byte(`{"usage":{"cost_in_usd_ticks":4800000000}}`)}
	require.InDelta(t, 0.48, taskActualChannelCostUSD(apikeyTask), 1e-9)

	apimartTask := &model.Task{
		Platform: constant.TaskPlatformApimartVideo,
		Data:     []byte(`{"data":{"cost":0.11472}}`),
	}
	require.InDelta(t, 0.11472, taskActualChannelCostUSD(apimartTask), 1e-9)
}
