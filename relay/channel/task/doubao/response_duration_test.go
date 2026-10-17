package doubao

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/stretchr/testify/require"
)

func TestTokenHubQueryStringDurationCompletesOriginalTask(t *testing.T) {
	// Production v2 response: resultSummary contains a string even though the
	// wrapper has a number. Preserve the tenant task ID and the actual seconds.
	raw := `{"id":"01a14977-905e-7407-ab15-cd3e069a297c","status":"succeeded","duration":10,"resultSummary":{"duration":"10","content":{"video_url":"https://example.com/original.mp4"},"resolution":"720p","usage":{"completion_tokens":216900,"total_tokens":216900}}}`
	resp, err := service.NormalizeTokenHubVideoResponse(&http.Response{Header: make(http.Header), Body: io.NopCloser(strings.NewReader(raw))})
	require.NoError(t, err)
	data, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	var body map[string]any
	require.NoError(t, common.Unmarshal(data, &body))
	require.Equal(t, "01a14977-905e-7407-ab15-cd3e069a297c", body["id"])
	require.Equal(t, float64(10), body["duration"])
	result, err := (&TaskAdaptor{}).ParseTaskResult(data)
	require.NoError(t, err)
	require.Equal(t, model.TaskStatusSuccess, result.Status)
	require.Equal(t, "https://example.com/original.mp4", result.Url)
	require.Equal(t, 10, result.BillableSeconds)
	require.Equal(t, 216900, result.TotalTokens)
}

func TestDoubaoResponseDurationValidation(t *testing.T) {
	for _, tc := range []struct {
		raw     string
		seconds int
	}{{`10`, 10}, {`"10"`, 10}, {`10.4`, 10}, {`"10.5"`, 11}} {
		t.Run(tc.raw, func(t *testing.T) {
			result, err := (&TaskAdaptor{}).ParseTaskResult([]byte(`{"status":"succeeded","duration":` + tc.raw + `,"content":{"video_url":"https://example.com/video.mp4"}}`))
			require.NoError(t, err)
			require.Equal(t, tc.seconds, result.BillableSeconds)
		})
	}
	for _, raw := range []string{`""`, `"abc"`, `"NaN"`, `"Inf"`, `true`, `{}`, `[]`, `null`, `0`, `-1`, `"-1"`, `0.1`, `1e100`} {
		t.Run("invalid_"+raw, func(t *testing.T) {
			_, err := (&TaskAdaptor{}).ParseTaskResult([]byte(`{"status":"succeeded","duration":` + raw + `,"content":{"video_url":"https://example.com/video.mp4"}}`))
			require.ErrorContains(t, err, "duration")
			_, err = service.NormalizeTokenHubVideoResponse(&http.Response{Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"status":"succeeded","duration":10,"resultSummary":{"duration":` + raw + `}}`))})
			require.ErrorContains(t, err, "duration")
		})
	}
	for _, raw := range []string{`{"status":"running","duration":null}`, `{"status":"running","duration":"0"}`, `{"status":"running"}`, `{"status":"failed","duration":0,"error":{"message":"rejected"}}`} {
		_, err := (&TaskAdaptor{}).ParseTaskResult([]byte(raw))
		require.NoError(t, err)
	}
}
