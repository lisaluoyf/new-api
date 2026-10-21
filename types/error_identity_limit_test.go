package types

import (
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

func TestOversizePublicErrorNeverRetainsOmittedPrivateFields(t *testing.T) {
	failure := &NewAPIError{PublicRedactionValues: []string{"HiddenVendor"}}
	// Different map iteration orders must never preserve an unsanitized field
	// from the original struct when the diagnostic byte budget is exhausted.
	for i := 0; i < 12; i++ {
		public := failure.sanitizePublicOpenAIError(OpenAIError{Code: "HiddenVendor.InvalidImage", Type: "HiddenVendor.ValidationError", Param: "HiddenVendor_private_parameter", Message: "HiddenVendor: " + strings.Repeat("reason ", 20000)})
		raw, err := common.Marshal(public)
		require.NoError(t, err)
		require.NotContains(t, string(raw), "HiddenVendor")
		require.NotNil(t, public.UpstreamError)
		require.True(t, public.UpstreamError.Truncated)
		require.Less(t, len(raw), common.MaxPublicUpstreamErrorBytes+1024)
	}
	public := failure.sanitizePublicClaudeError(ClaudeError{Type: "HiddenVendor.ValidationError", Message: "HiddenVendor: " + strings.Repeat("reason ", 20000)})
	raw, err := common.Marshal(public)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "HiddenVendor")
	require.NotNil(t, public.UpstreamError)
	require.True(t, public.UpstreamError.Truncated)
}

func TestPublicErrorRejectsOversizedEnvelopeWithExplicitTruncation(t *testing.T) {
	failure := &NewAPIError{PublicRedactionValues: []string{"HiddenVendor"}}
	public := failure.sanitizePublicOpenAIError(OpenAIError{Code: "HiddenVendor.InvalidImage", Message: "HiddenVendor " + strings.Repeat("x", (2<<20)+1)})
	require.NotNil(t, public.UpstreamError)
	require.True(t, public.UpstreamError.Truncated)
	raw, err := common.Marshal(public)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "HiddenVendor")
	require.Less(t, len(raw), common.MaxPublicUpstreamErrorBytes)
}
