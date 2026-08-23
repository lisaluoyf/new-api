package apimartvideo

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsMotionControlModel(t *testing.T) {
	require.True(t, IsMotionControlModel("kling-v3-motion-control"))
	require.True(t, IsVideoModel("kling-v3-motion-control"))
	require.True(t, IsVideoModel(ModelDoubaoSeedance20))
	require.True(t, IsVideoModel(ModelKlingV3Omni))
	require.False(t, IsMotionControlModel("sora-2"))
	require.False(t, IsMotionControlModel(ModelDoubaoSeedance20))
}

func TestKlingOmniBillingVariant(t *testing.T) {
	tests := []struct {
		mode     string
		audio    bool
		hasVideo bool
		want     string
	}{
		{mode: "std", want: "base"},
		{mode: "std", audio: true, want: "sound"},
		{mode: "std", audio: true, hasVideo: true, want: "video"},
		{mode: "pro", want: "pro"},
		{mode: "pro", audio: true, want: "pro-sound"},
		{mode: "pro", hasVideo: true, want: "pro-video"},
		{mode: "4K", want: "4k"},
		{mode: "4k", audio: true, want: "4k-sound"},
		{mode: "4k", hasVideo: true, want: "4k"},
	}
	for _, tc := range tests {
		require.Equal(t, tc.want, klingOmniBillingVariant(tc.mode, tc.audio, tc.hasVideo))
	}
}

func TestModeBillingRatio(t *testing.T) {
	require.InDelta(t, 1.0, modeBillingRatio("std"), 1e-9)
	require.InDelta(t, ProUSDPerSecond/StdUSDPerSecond, modeBillingRatio("pro"), 1e-9)
}

func TestDefaultBillableSeconds(t *testing.T) {
	require.Equal(t, 10, defaultBillableSeconds("image"))
	require.Equal(t, 30, defaultBillableSeconds("video"))
}
