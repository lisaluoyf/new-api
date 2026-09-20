package constant

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMultipartAsyncImageRequestRoutesToEdits(t *testing.T) {
	t.Parallel()

	contentType := "multipart/form-data; boundary=test"
	require.Equal(t, "/v1/images/edits", EffectiveImageRequestPath("/v1/images/generations/async", contentType))
	require.Equal(t, RelayModeImagesEdits, Request2RelayMode("/v1/images/generations/async", contentType))
}

func TestJSONAsyncImageRequestRemainsGeneration(t *testing.T) {
	t.Parallel()

	require.Equal(t, "/v1/images/generations/async", EffectiveImageRequestPath("/v1/images/generations/async", "application/json"))
	require.Equal(t, RelayModeImagesGenerations, Request2RelayMode("/v1/images/generations/async", "application/json"))
}

func TestAsyncEditsPreservesOperationAndClientPath(t *testing.T) {
	for _, contentType := range []string{"multipart/form-data; boundary=test", "application/json"} {
		path := "/v1/images/edits/async"
		require.Equal(t, path, EffectiveImageRequestPath(path, contentType))
		require.Equal(t, RelayModeImagesEdits, Request2RelayMode(path, contentType))
		require.True(t, IsAsyncImageRequestPath(path))
		require.True(t, IsImageEditsPath(path))
	}
	require.False(t, IsAsyncImageRequestPath("/v1/images/edits"))
	require.False(t, IsImageEditsPath("/v1/images/generations/async"))
}
