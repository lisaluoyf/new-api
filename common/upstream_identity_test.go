package common

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIdentityRedactionKeepsActionableFailure(t *testing.T) {
	raw := []byte(`{"provider":{"name":"Hidden Labs"},"vendor":"UnknownRelay AI","endpoint":"https://api.private-route.ai/tasks","model":"deployment-secret-model","error":{"code":"HiddenLabs.UnsupportedImageFormat","message":"HIDDEN LABS and UnknownRelay rejected deployment-secret-model at api.private-route.ai: image[0] must be JPEG; actual video/mp4","details":{"field":"image[0]","expected":"image/jpeg","actual":"video/mp4"}}}`)
	values := UpstreamIdentityValues("HiddenLabs-video", "https://api.private-route.ai", "deployment-secret-model", "public-video")
	result := NewPublicUpstreamError(400, raw, nil, values...)
	encoded, err := Marshal(result)
	require.NoError(t, err)
	for _, name := range []string{"hiddenlabs", "hidden labs", "unknownrelay", "private-route", "deployment-secret-model"} {
		require.NotContains(t, strings.ToLower(string(encoded)), name)
	}
	for _, reason := range []string{"UnsupportedImageFormat", "image[0]", "image/jpeg", "video/mp4"} {
		require.Contains(t, string(encoded), reason)
	}
}

func TestNewChannelRoutingAliasesAreDynamicAndCaseInsensitive(t *testing.T) {
	values := UpstreamIdentityValues("FreshVendor-video", "https://new-route.example.com", "tenant-deployment-4", "public-model")
	text := SanitizeUpstreamErrorText("fReShVeNdOr: tenant-deployment-4 rejected image[0] with invalid aspect_ratio", values...)
	require.NotContains(t, strings.ToLower(text), "freshvendor")
	require.NotContains(t, text, "tenant-deployment-4")
	require.Contains(t, text, "image[0]")
	require.Contains(t, text, "aspect_ratio")
	require.Equal(t, "UnsupportedImageFormat", PublicDiagnosticCode("FreshVendor.UnsupportedImageFormat", values...))
	require.Equal(t, "InvalidParameter.UnsupportedImageFormat", PublicDiagnosticCode("InvalidParameter.UnsupportedImageFormat", values...))
	require.Equal(t, "InputVideoSensitiveContentDetected.PolicyViolation", PublicDiagnosticCode("InputVideoSensitiveContentDetected.PolicyViolation", values...))
}

func TestShortChannelAliasDoesNotDestroyWords(t *testing.T) {
	values := UpstreamIdentityValues("HS-Seedance", "", "", "seedance-2.5")
	text := SanitizeUpstreamErrorText("HS hs trace_HS_123 HS: threshold must be lower", values...)
	require.NotContains(t, text, "HS")
	require.NotContains(t, text, " hs ")
	require.Contains(t, text, "threshold must be lower")
}

func TestEmbeddedErrorIdentityFieldsDoNotBypassRedaction(t *testing.T) {
	original := `wrapped error: {"provider":"HiddenRelay","error":{"code":"HiddenRelay.InvalidImage","message":"HiddenRelay rejected image[0]"}}`
	detail := NewPublicUpstreamError(400, []byte(original), nil)
	encoded, err := Marshal(detail)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "HiddenRelay")
	require.Contains(t, string(encoded), "image[0]")
}

func TestUnconfiguredInternalModelAndProviderRequestIDArePrivate(t *testing.T) {
	raw := []byte(`{"model_name":"internal-unconfigured-9","provider":"NewRelay","error":{"message":"NewRelay rejected internal-unconfigured-9: image[0] must be JPEG","code":"NewRelay.InvalidImage"}}`)
	detail := NewPublicUpstreamError(400, raw, map[string][]string{"X-Request-Id": {"NewRelay-private-request"}})
	encoded, err := Marshal(detail)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "NewRelay")
	require.NotContains(t, string(encoded), "internal-unconfigured-9")
	require.Empty(t, detail.RequestID)
	require.Contains(t, string(encoded), "image[0] must be JPEG")
}

func TestIdentityRedactionNeverLeavesAuthorizationCredentialTail(t *testing.T) {
	for _, source := range []string{"unreachable https://private.example/a Authorization: Bearer hidden-bearer-secret", "Authorization: Basic hidden-basic-secret; vendor=HiddenRelay"} {
		text := SanitizeUpstreamErrorText(source)
		require.NotContains(t, text, "hidden-bearer-secret")
		require.NotContains(t, text, "hidden-basic-secret")
		require.NotContains(t, text, "HiddenRelay")
	}
}
