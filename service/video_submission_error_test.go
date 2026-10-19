package service

import (
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
)

func TestMaterialSubmitErrorsRetainActionableClassification(t *testing.T) {
	for _, tc := range []struct {
		status              int
		body, code, message string
	}{
		{409, `{"error":{"code":"ASSET_CERTIFICATION_REQUIRED","message":"private diagnostic"}}`, "asset_certification_required", "complete material certification"},
		{502, `{"error":{"code":"PROVIDER_REQUEST_REJECTED","message":"material failed: input image may be related to copyright restrictions. Bearer private-key https://private.example/a"}}`, "content_policy_violation", "copyright review"},
		{502, `{"error":{"code":"PROVIDER_REQUEST_REJECTED","message":"input image may contain sensitive information"}}`, "content_policy_violation", "content safety review"},
	} {
		err := PublicVideoSubmitError(tc.status, []byte(tc.body), "private-key", 273)
		require.Equal(t, tc.code, err.Code)
		require.Equal(t, tc.status, err.StatusCode)
		require.True(t, err.LocalError)
		require.Contains(t, err.Message, tc.message)
		require.NotContains(t, err.Message, "private")
	}
	for _, body := range []string{`{broken`, `{"error":{"code":"OTHER","message":"may be related to copyright restrictions"}}`, `{"error":{"code":"PROVIDER_REQUEST_REJECTED","message":"database connection failed"}}`} {
		err := PublicVideoSubmitError(502, []byte(body), "", 273)
		require.Equal(t, "video_service_error", err.Code)
		require.False(t, err.LocalError)
	}
}

func TestCertificationRejectionRequiresNoTaskIdentity(t *testing.T) {
	clear := []byte(`{"error":{"code":"ASSET_CERTIFICATION_REQUIRED","message":"certification required"}}`)
	require.True(t, SeedanceExplicitSubmissionRejection(409, clear))
	for _, body := range []string{
		`{"id":"original","error":{"code":"ASSET_CERTIFICATION_REQUIRED"}}`,
		`{"error":{"code":"ASSET_CERTIFICATION_REQUIRED","details":{"taskId":"original"}}}`,
		`{"error":{"code":"ASSET_CERTIFICATION_REQUIRED","details":{"task_id":"original"}}}`,
	} {
		require.False(t, SeedanceExplicitSubmissionRejection(409, []byte(body)))
	}
	require.False(t, SeedanceExplicitSubmissionRejection(502, []byte(`{"error":{"code":"PROVIDER_REQUEST_REJECTED","message":"copyright restrictions"}}`)))
	require.False(t, SeedanceExplicitSubmissionRejection(409, []byte(`{"error":{"code":"OTHER"}}`)))
}

func TestSubmissionReceiptExposesOnlySafeError(t *testing.T) {
	row := &model.VideoSubmission{Status: model.VideoSubmissionUnknown, ErrorStatus: 502,
		ErrorCode: "video_service_error", ErrorMessage: "failed https://private.example/image.png Authorization: Bearer secret", ErrorReference: "provider-private-id"}
	err := VideoSubmissionPublicError(row)
	require.Equal(t, "video_service_error", err["code"])
	require.NotContains(t, err["message"], "private.example")
	require.NotContains(t, err["message"], "secret")
	row.Status = model.VideoSubmissionAccepted
	require.Nil(t, VideoSubmissionPublicError(row))
}
