package service

import (
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestImageSubmissionScopeHasNoModelAllowlistAndOnlyPreExecutionProvesZero(t *testing.T) {
	for _, path := range []string{"/v1/edits", "/v1/images/generations", "/v1/images/generations/async", "/v1/images/edits", "/v1/images/edits/async", "/v1/images/variations", "/v1/midjourney/generations", "/v1/midjourney/generations/imagine", "/mj/submit/imagine", "/fast/mj/submit/blend", "/v1beta/models/future-model:generateContent", "/v1/models/future-model:predict", "/v1/chat/completions", "/v1/responses"} {
		require.True(t, IsImageSubmissionPath(path), path)
	}
	for _, path := range []string{"/v1/videos", "/v1/tasks/task_id", "/v1/models", "/mj/task/id/fetch", "/v1/embeddings"} {
		require.False(t, IsImageSubmissionPath(path), path)
	}
	for _, status := range []string{"pending", "submitting", "accepted", "acceptance_unknown", "not_accepted"} {
		dto := ImageSubmissionDTO(&model.ImageSubmission{Status: status, Deadline: time.Now().Unix() + 60})
		safe := status == "not_accepted"
		require.Equal(t, safe, dto["retry_safe"])
		require.Equal(t, safe, dto["billing_final"])
		if !safe {
			require.Nil(t, dto["net_charge"])
		}
	}
}
