package sora

import (
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
)

func TestParseTaskResultApikeyPending(t *testing.T) {
	adaptor := &TaskAdaptor{}
	info, err := adaptor.ParseTaskResult([]byte(`{"status":"pending","progress":10}`))
	require.NoError(t, err)
	require.Equal(t, model.TaskStatusQueued, info.Status)
	require.Equal(t, "10%", info.Progress)
}

func TestParseTaskResultApikeyDone(t *testing.T) {
	adaptor := &TaskAdaptor{}
	info, err := adaptor.ParseTaskResult([]byte(`{
		"status":"done",
		"progress":100,
		"usage":{"cost_in_usd_ticks":4800000000},
		"video":{"duration":6,"url":"/v1/videos/request-id/content"}
	}`))
	require.NoError(t, err)
	require.Equal(t, model.TaskStatusSuccess, info.Status)
	require.Equal(t, 6, info.BillableSeconds)
}

func TestIsGrokVideoModel(t *testing.T) {
	require.True(t, isGrokVideoModel("grok-imagine-video-1.5"))
	require.False(t, isGrokVideoModel("sora-2"))
}
