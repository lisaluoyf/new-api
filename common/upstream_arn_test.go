package common

import (
	"github.com/stretchr/testify/require"
	"net/http"
	"testing"
)

func TestUpstreamErrorHidesSDKResourceARNs(t *testing.T) {
	for _, arn := range []string{"arn:aws:bedrock:us-east-1:123456789012:inference-profile/private-route", "arn:aws-cn:bedrock:cn-north-1:123456789012:foundation-model/private-model", "arn:aws:s3:::private-bucket/image.png"} {
		raw, err := Marshal(map[string]any{"message": "Access denied for " + arn + "; expected image/jpeg", "resource_name": arn})
		require.NoError(t, err)
		detail := NewPublicUpstreamError(403, raw, http.Header{"X-Request-Id": []string{arn}})
		wire, err := Marshal(detail)
		require.NoError(t, err)
		for _, secret := range []string{arn, "123456789012", "private-route", "private-model", "private-bucket"} {
			require.NotContains(t, string(wire), secret)
		}
		require.Contains(t, string(wire), "expected image/jpeg")
	}
}
