package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

func TestTaskUsageBackfillDoesNotMatchReferencedDraft(t *testing.T) {
	setupUserLogVisibilityTestDB(t)
	draft := Log{UserId: 1, Type: LogTypeConsume, CompletionTokens: 48400, Other: `{"task_id":"task_draft","request_data":{"draft":true}}`}
	require.NoError(t, LOG_DB.Create(&draft).Error)
	for i := 0; i < 101; i++ {
		upgrade := Log{UserId: 1, Type: LogTypeConsume, CompletionTokens: 245025, Other: common.MapToJsonStr(map[string]any{"task_id": "task_final", "request_data": map[string]any{"draft_task_id": "task_draft"}})}
		require.NoError(t, LOG_DB.Create(&upgrade).Error)
	}
	require.NoError(t, UpdateLogResultByTaskID(1, "task_draft", 0, map[string]any{"usage": map[string]any{"completion_tokens": int64(48400)}}))
	var logs []Log
	require.NoError(t, LOG_DB.Order("id").Find(&logs).Error)
	require.Equal(t, 48400, logs[0].CompletionTokens)
	for _, log := range logs[1:] {
		require.Equal(t, 245025, log.CompletionTokens)
		require.NotContains(t, log.Other, `"usage"`)
	}
}
