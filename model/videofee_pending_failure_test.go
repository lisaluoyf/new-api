package model

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestGenericVideoFeePendingFailureReason(t *testing.T) {
	for _, reason := range []string{"", "Video generation failed", "原因待核查", " 原因待核查（任务 60f0509c-9519-475d-9ef7-7e5915d2ce6d） ", "原因待核查（任务 [redacted]）", "原因待核查(任务 upstream-id)"} {
		require.True(t, IsGenericTaskFailureReason(reason), reason)
	}
	for _, reason := range []string{"The request failed because the output video may be related to copyright restrictions.", "InputImageSensitiveContentDetected", "原因待核查：版权审核失败", "原因待核查（任务 up） Copyright restricted", "原因待核查（任务 line\nbreak）"} {
		require.False(t, IsGenericTaskFailureReason(reason), reason)
	}
}
