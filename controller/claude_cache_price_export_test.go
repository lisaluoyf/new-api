package controller

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestClaudeCache1hExportPreservesRateAndExplicitZero(t *testing.T) {
	base := 5.0
	zero := 0.0
	require.Nil(t, exportCache1hPrice("claude-opus-5-5", nil, nil, .1))
	got := exportCache1hPrice("claude-opus-5-5", &base, nil, .1)
	require.NotNil(t, got)
	require.InDelta(t, .8, *got, 1e-12)
	got = exportCache1hPrice("claude-opus-5-5", &base, &zero, .1)
	require.NotNil(t, got)
	require.Zero(t, *got)
}
