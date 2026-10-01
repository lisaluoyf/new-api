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

func TestTaskResultBackfillPrefersOriginalChargeOverAdjustment(t *testing.T) {
	setupUserLogVisibilityTestDB(t)
	original := Log{UserId: 1, Type: LogTypeConsume, Quota: 1000, Other: `{"is_task":true,"task_id":"task_public"}`}
	adjustment := Log{UserId: 1, Type: LogTypeConsume, Quota: 2, Other: `{"task_id":"task_public","pre_consumed_quota":1000,"actual_quota":1002}`}
	require.NoError(t, LOG_DB.Create(&original).Error)
	require.NoError(t, LOG_DB.Create(&adjustment).Error)
	row, err := FindConsumeLogRowForTask(1, "task_public")
	require.NoError(t, err)
	require.Equal(t, original.Id, row.Id)
	require.NoError(t, UpdateLogResultByTaskID(1, "task_public", 42, map[string]any{"result_url": "/v1/videos/task_public/content"}))
	require.NoError(t, LOG_DB.First(&original, original.Id).Error)
	require.NoError(t, LOG_DB.First(&adjustment, adjustment.Id).Error)
	require.Equal(t, 42, original.UseTime)
	require.Equal(t, 0, adjustment.UseTime)
	require.Contains(t, original.Other, "result_url")
	require.NotContains(t, adjustment.Other, "result_url")
}
