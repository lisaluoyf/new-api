package service

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
	"net/http"
	"testing"
)

func TestBeeNexOperationErrorsAndResourceModeration(t *testing.T) {
	cases := []struct {
		name, action, body string
		http, want         int
	}{
		{"invalid_group", "CreateAsset", `{"ResponseMetadata":{"RequestId":"request-1"},"Result":{"Error":{"Code":"InvalidParameter.GroupId","Message":"invalid group private-secret"}}}`, 200, 400},
		{"rate_limit", "CreateAsset", `{"Result":{"Error":{"Code":"AccountFlowLimitExceeded","Message":"rate limited"}}}`, 200, 429},
		{"active_empty_error", "GetAsset", `{"Result":{"Id":"asset-1","Status":"Active","Error":{"Code":"","Message":""}}}`, 200, 0},
		{"failed_resource", "GetAsset", `{"Result":{"Id":"asset-1","Status":"Failed","Error":{"Code":"ModerationRejected","Message":"review failed"}}}`, 200, 0},
		{"unknown_transport", "CreateAsset", `{"error":{"message":"private-secret unavailable"}}`, 503, 502},
		{"normal_create", "CreateAsset", `{"Result":{"Id":"asset-1"}}`, 200, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var envelope map[string]any
			require.NoError(t, common.Unmarshal([]byte(tc.body), &envelope))
			err := beeNexProtocolError(tc.action, tc.http, envelope, []byte(tc.body), http.Header{}, "private-secret")
			if tc.want == 0 {
				require.NoError(t, err)
				return
			}
			var failure *SeedanceAPIError
			require.ErrorAs(t, err, &failure)
			require.Equal(t, tc.want, failure.Status)
			require.NotContains(t, failure.Message, "private-secret")
			require.NotNil(t, failure.UpstreamError)
			if tc.name == "invalid_group" {
				require.Contains(t, failure.Message, "InvalidParameter.GroupId")
				require.Contains(t, failure.Message, "request-1")
			}
			if tc.name == "unknown_transport" {
				require.Equal(t, "submission_unknown", failure.Code)
			}
		})
	}
}
