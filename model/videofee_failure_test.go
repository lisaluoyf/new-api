package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

func TestVideoFeeStructuredMaterialFailure(t *testing.T) {
	for _, malformed := range []bool{false, true} {
		reason := `upstream material api failed: volc api http 400: {"ResponseMetadata":{"Action":"CreateAsset","Error":{"Code":"[redacted]","Message":"Width must be between 300px and 6000px."}}}`
		if malformed {
			reason = `upstream material api failed: volc api http 400: {"ResponseMetadata":{,"Action":"CreateAsset","Error":{"Code":"[redacted]","Message":"Width must be between 300px and 6000px."}}}`
		}
		raw, err := common.Marshal(map[string]any{"code": "success", "data": map[string]any{"status": "FAILURE", "error_code": "upstream_material_api_failed", "fail_reason": reason}})
		require.NoError(t, err)
		task := &Task{Platform: "videofee", Status: TaskStatusFailure, Properties: Properties{OriginModelName: "seedance-2.5"}, FailReason: "Video generation failed", Data: raw}
		public := PublicTaskError(task)
		require.Equal(t, "upstream_material_api_failed", public.Code)
		require.Equal(t, "Width must be between 300px and 6000px.", public.Message)
		require.Equal(t, "Video generation failed", task.FailReason)
		require.Equal(t, raw, []byte(task.Data))
	}
}

func TestVideoFeeFailureFieldsAndSafetyBoundaries(t *testing.T) {
	for _, tc := range []struct{ body, code, message string }{
		{`{"data":{"status":"FAILURE","error_code":"TASK_FAILED","fail_reason":""}}`, "", ""},
		{`{"data":{"status":"IN_PROGRESS","error_code":"InvalidParameter","fail_reason":"stale failure"}}`, "", ""},
		{`{"data":{"status":"FAILURE","error_code":"UnknownProviderSecret","fail_reason":"Image width is invalid"}}`, "task_failed", "Image width is invalid"},
		{`{"data":{"status":"FAILED","error_code":"InvalidParameter","fail_reason":"Image width is invalid"}}`, "InvalidParameter", "Image width is invalid"},
		{`{"data":{"status":"FAILURE","error_code":"upstream_material_api_failed","fail_reason":"upstream material api failed: volc api http 400: {invalid}"}}`, "upstream_material_api_failed", ""},
	} {
		code, message := VideoFeeFailureDetails([]byte(tc.body))
		require.Equal(t, tc.code, code)
		require.Equal(t, tc.message, message)
	}
	body := []byte(`{"data":{"status":"FAILURE","error_code":"upstream_material_api_failed","fail_reason":"Width invalid; Bearer private-secret https://private.example/image.png"}}`)
	task := &Task{Platform: "videofee", Status: TaskStatusFailure, Properties: Properties{OriginModelName: "seedance-2.5"}, Data: body}
	public := PublicTaskError(task)
	require.Contains(t, public.Message, "Width invalid")
	require.NotContains(t, public.Message, "private-secret")
	require.NotContains(t, public.Message, "private.example")
}
