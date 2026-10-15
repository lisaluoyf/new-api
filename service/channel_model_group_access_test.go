package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/stretchr/testify/require"
)

func TestModelShareGroupAccessRequiresProbe(t *testing.T) {
	previous := common.AutomaticDisableChannelEnabled
	common.AutomaticDisableChannelEnabled = true
	t.Cleanup(func() {
		common.AutomaticDisableChannelEnabled = previous
		ClearChannelHealth(67)
	})

	// Replay the real provider response through the production error parser.
	body := `{"error":{"type":"<nil>","message":"Model 'claude-haiku-4-5-20251001' is not enabled for your selected share group. Please allow this group for the model (Model Pricing -> Groups) or select a different share group."},"type":"error"}`
	for _, status := range []int{http.StatusBadRequest, http.StatusForbidden, http.StatusNotFound} {
		err := RelayErrorHandler(context.Background(), &http.Response{
			StatusCode: status,
			Body:       io.NopCloser(strings.NewReader(body)),
		}, false)
		require.Equal(t, CategoryProbeBeforeDisable, ClassifyChannelError(err))
		require.False(t, IsHighConfidenceRecharge(err))
		action, reason := EvaluateChannelHealth(types.ChannelError{ChannelId: 67, AutoBan: true}, err)
		require.Equal(t, HealthProbeBeforeDisable, action)
		require.NotEmpty(t, reason)
		action, _ = EvaluateChannelHealth(types.ChannelError{ChannelId: 67, AutoBan: false}, err)
		require.Equal(t, HealthSkip, action)
		common.AutomaticDisableChannelEnabled = false
		action, _ = EvaluateChannelHealth(types.ChannelError{ChannelId: 67, AutoBan: true}, err)
		require.Equal(t, HealthSkip, action)
		common.AutomaticDisableChannelEnabled = true
	}
}

func TestModelShareGroupAccessBoundaries(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		message string
		status  int
		want    ChannelErrorCategory
	}{
		{"case insensitive", "MODEL 'haiku' IS NOT ENABLED FOR YOUR SELECTED SHARE GROUP.", 400, CategoryProbeBeforeDisable},
		{"ordinary parameter error", "Model 'haiku': invalid request parameter", 400, CategorySkip},
		{"user selects group", "Invalid selected share group for model 'haiku'", 400, CategorySkip},
		{"non model access", "Feature is not enabled for your selected share group", 400, CategorySkip},
		{"unrelated model setting", "Model 'haiku' is not enabled for streaming", 400, CategorySkip},
		{"unsupported status", "Model 'haiku' is not enabled for your selected share group", 422, CategorySkip},
		{"user quota priority", "用户额度不足: Model 'haiku' is not enabled for your selected share group", 400, CategorySkip},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := types.WithOpenAIError(types.OpenAIError{Message: tc.message, Type: "upstream_error"}, tc.status)
			require.Equal(t, tc.want, ClassifyChannelError(err))
		})
	}
	// Diagnostic metadata alone is not an upstream model-access diagnosis.
	err := types.WithOpenAIError(types.OpenAIError{Message: "invalid request parameter", Type: "upstream_error"}, 400)
	err.UpstreamResponseBody = `{"metadata":{"message":"Model 'haiku' is not enabled for your selected share group"}}`
	require.Equal(t, CategorySkip, ClassifyChannelError(err))
}
