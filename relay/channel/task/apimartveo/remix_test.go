package apimartveo

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestVeoRemixUsesCanonicalProviderModelAndPrivateOrigin(t *testing.T) {
	old := model.DB
	t.Cleanup(func() { model.DB = old })
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	require.NoError(t, db.AutoMigrate(&model.Task{}))
	require.NoError(t, db.Create(&model.Task{TaskID: "task_public_origin", UserId: 42, ChannelId: 306, Status: model.TaskStatusSuccess, Properties: model.Properties{OriginModelName: Fast}, PrivateData: model.TaskPrivateData{UpstreamTaskID: "provider_origin"}}).Error)
	a, c, info := fixture(t, Fast, "veo3.1-fast", `,"raw":false`)
	info.UserId = 42
	info.ChannelId = 306
	info.Action = constant.TaskActionRemix
	info.OriginTaskID = "task_public_origin"
	require.Nil(t, a.ValidateRequestAndSetAction(c, info))
	body, err := a.BuildRequestBody(c, info)
	require.NoError(t, err)
	var got map[string]any
	require.NoError(t, common.DecodeJson(body, &got))
	require.Equal(t, "veo3.1-fast", got["model"])
	require.Equal(t, false, got["raw"])
	require.NotContains(t, got, "duration")
	require.NotContains(t, got, "generate_audio")
	u, err := a.BuildRequestURL(info)
	require.NoError(t, err)
	require.Equal(t, "https://api.apimart.ai/v1/videos/provider_origin/remix", u)
	require.Equal(t, 8.0, a.EstimateBilling(c, info)["seconds"])
}
