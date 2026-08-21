package service

import (
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestPreserveTaskUpstreamCost(t *testing.T) {
	t.Parallel()

	previous := []byte(`{"data":{"cost":1.28568,"credits_cost":12.8568,"status":"processing"}}`)
	tests := []struct {
		name     string
		platform constant.TaskPlatform
		status   string
		current  string
		wantCost float64
	}{
		{
			name:     "successful response omits cost",
			platform: constant.TaskPlatformApimartVideo,
			status:   string(model.TaskStatusSuccess),
			current:  `{"data":{"status":"completed"}}`,
			wantCost: 1.28568,
		},
		{
			name:     "new positive cost wins",
			platform: constant.TaskPlatformApimartVideo,
			status:   string(model.TaskStatusSuccess),
			current:  `{"data":{"cost":2.5,"status":"completed"}}`,
			wantCost: 2.5,
		},
		{
			name:     "failure does not restore prior cost",
			platform: constant.TaskPlatformApimartVideo,
			status:   string(model.TaskStatusFailure),
			current:  `{"data":{"cost":0,"status":"failed"}}`,
			wantCost: 0,
		},
		{
			name:     "other platform is unchanged",
			platform: constant.TaskPlatformOpenAIImage,
			status:   string(model.TaskStatusSuccess),
			current:  `{"data":{"status":"completed"}}`,
			wantCost: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := preserveTaskUpstreamCost(tc.platform, tc.status, previous, []byte(tc.current))
			require.InDelta(t, tc.wantCost, gjson.GetBytes(got, "data.cost").Float(), 1e-9)
			if tc.wantCost == 1.28568 {
				require.InDelta(t, 12.8568, gjson.GetBytes(got, "data.credits_cost").Float(), 1e-9)
			}
		})
	}
}
