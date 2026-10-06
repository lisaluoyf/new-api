package doubao

import (
	"io"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestSeedanceOutboundAssetsResolveToOriginalChannelAndKey(t *testing.T) {
	oldDB := model.DB
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	t.Cleanup(func() { model.DB = oldDB })
	require.NoError(t, db.AutoMigrate(&model.SeedanceResource{}))
	asset := model.SeedanceResource{ID: "asset_public", Kind: "asset", UserID: 1, ChannelID: 283, KeyFingerprint: service.SeedanceKeyFingerprint("inference-key"), UpstreamID: "asset-native-private", Status: "Active"}
	require.NoError(t, db.Create(&asset).Error)
	c, _ := seedanceContext(`{"model":"seedance-2.5","prompt":"tea","duration":4,"resolution":"480p","image_urls":["asset://asset_public"],"generate_audio":false,"watermark":false,"seed":0}`)
	c.Set("id", 1)
	info := &relaycommon.RelayInfo{OriginModelName: "seedance-2.5", TaskRelayInfo: &relaycommon.TaskRelayInfo{}, ChannelMeta: &relaycommon.ChannelMeta{ChannelId: 283, ApiKey: "inference-key", IsModelMapped: true, UpstreamModelName: "dreamina-seedance-2-5-260628"}}
	a := &TaskAdaptor{}
	a.Init(info)
	require.Nil(t, a.ValidateRequestAndSetAction(c, info))
	body, err := a.BuildRequestBody(c, info)
	require.NoError(t, err)
	raw, err := io.ReadAll(body)
	require.NoError(t, err)
	require.Contains(t, string(raw), "asset://asset-native-private")
	require.NotContains(t, string(raw), "asset://asset_public")
	var fields map[string]any
	require.NoError(t, common.Unmarshal(raw, &fields))
	require.Equal(t, false, fields["generate_audio"])
	require.Equal(t, false, fields["watermark"])
	require.Equal(t, float64(0), fields["seed"])
	require.Equal(t, "dreamina-seedance-2-5-260628", fields["model"])
	req, err := relaycommon.GetTaskRequest(c)
	require.NoError(t, err)
	saved, err := common.Marshal(req)
	require.NoError(t, err)
	require.Contains(t, string(saved), "asset://asset_public")
	require.NotContains(t, string(saved), "asset-native-private")
	c.Set("id", 2)
	_, err = a.BuildRequestBody(c, info)
	require.ErrorContains(t, err, "not found")
	c.Set("id", 1)
	info.ChannelId = 284
	_, err = a.BuildRequestBody(c, info)
	require.Error(t, err)
	info.ChannelId = 283
	a.apiKey = "another-key"
	_, err = a.BuildRequestBody(c, info)
	require.Error(t, err)
}
