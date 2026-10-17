package service

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestSeedanceExplicitParameterRejection(t *testing.T) {
	for _, tc := range []struct {
		status   int
		body     string
		rejected bool
	}{
		{400, `{"error":{"code":"InvalidParameter","message":"role must be specified for image contents"},"requestId":"diagnostic-request"}`, true},
		{400, `{"id":"already-accepted","error":{"code":"InvalidParameter","message":"invalid"}}`, false},
		{400, `{"data":{"task_id":"accepted"},"error":{"code":"InvalidParameter","message":"invalid"}}`, false},
		{400, `{"error":{"code":"UnknownError","message":"invalid"}}`, false},
		{400, `bad gateway`, false},
		{503, `{"error":{"code":"InvalidParameter","message":"invalid"}}`, false},
		{200, `{"error":{"code":"InvalidParameter","message":"invalid"}}`, false},
		{429, `{"error":{"message":"rate limit exceeded"}}`, true},
	} {
		require.Equal(t, tc.rejected, SeedanceExplicitSubmissionRejection(tc.status, []byte(tc.body)), tc.body)
	}
}
