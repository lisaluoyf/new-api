package service

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
)

func TestBeeNexNewAssetReadAfterWrite(t *testing.T) {
	body := []byte(`{"Result":{"Error":{"Code":"InvalidAsset","Message":"not yet available"}}}`)
	var envelope map[string]any
	require.NoError(t, common.Unmarshal(body, &envelope))
	failure := beeNexProtocolError("GetAsset", 200, envelope, body, http.Header{})
	resource := &model.SeedanceResource{ChannelID: 274, Kind: "asset", Status: "Processing", UpstreamID: "asset-native", CreatedAt: time.Now().Unix()}
	fields := map[string]any{"Id": resource.UpstreamID}
	calls := 0
	result, err := beeNexRetryNewAssetRead(context.Background(), resource, "GetAsset", fields, func() (map[string]any, error) {
		calls++
		if calls == 1 {
			return nil, failure
		}
		return map[string]any{"Result": map[string]any{"Id": resource.UpstreamID, "Status": "Active"}}, nil
	})
	require.NoError(t, err)
	require.Equal(t, 2, calls)
	require.Equal(t, "Active", seedanceString(seedanceResult(result), "Status"))
	for _, action := range []string{"CreateAsset", "CreateAssetGroup", "DeleteAsset", "ListAssets"} {
		require.False(t, beeNexNewAssetReadPending(resource, action, fields, failure))
	}
	old := *resource
	old.CreatedAt -= 120
	require.False(t, beeNexNewAssetReadPending(&old, "GetAsset", fields, failure))
	require.False(t, beeNexNewAssetReadPending(resource, "GetAsset", map[string]any{"Id": "other-asset"}, failure))
	resource.Status = "Failed"
	require.False(t, beeNexNewAssetReadPending(resource, "GetAsset", fields, failure))
}
