package relay

import (
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relay/channel/task/apimartvideo"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestSeedance20SelectsApimartBeforeModelMapping(t *testing.T) {
	for _, name := range []string{"seedance-2.0", "seedance-2.0-fast", "seedance-2.0-mini", "doubao-seedance-2.0"} {
		info := &relaycommon.RelayInfo{
			OriginModelName: name,
			ChannelMeta:     &relaycommon.ChannelMeta{ChannelBaseUrl: "https://api.apib.ai", ChannelType: constant.ChannelTypeOpenAI},
		}
		c, _ := gin.CreateTestContext(nil)
		require.IsType(t, &apimartvideo.TaskAdaptor{}, ResolveTaskAdaptor(c, constant.TaskPlatform("sora"), info))
		require.Equal(t, constant.TaskPlatform(constant.TaskPlatformApimartVideo), ResolveTaskPlatform(c, constant.TaskPlatform("sora"), info))
	}
}

func TestVideoFeeScopeIsChannel273AndEnabledModelsOnly(t *testing.T) {
	for _, id := range []int{273, 272, 999} {
		for _, name := range []string{"seedance-2.0", "seedance-2.5", "seedance-2.0-mini", "MiniMax-H3", "sora-2"} {
			info := &relaycommon.RelayInfo{OriginModelName: name, ChannelMeta: &relaycommon.ChannelMeta{ChannelId: id, ChannelBaseUrl: "https://seedance2026.vip", ChannelType: constant.ChannelTypeOpenAI}}
			c, _ := gin.CreateTestContext(nil)
			platform := ResolveTaskPlatform(c, "1", info)
			require.Equal(t, id == 273 && (name == "seedance-2.0" || name == "seedance-2.5"), platform == constant.TaskPlatformVideoFee)
		}
	}
}

func TestBeeNexScope(t *testing.T) {
	for _, id := range []int{274, 273, 272} {
		for _, name := range []string{"seedance-2.0", "seedance-2.5", "seedance-2.0-mini", "sora-2"} {
			info := &relaycommon.RelayInfo{OriginModelName: name, ChannelMeta: &relaycommon.ChannelMeta{ChannelId: id, ChannelType: constant.ChannelTypeOpenAI}}
			c, _ := gin.CreateTestContext(nil)
			want := id == 274 && (name == "seedance-2.0" || name == "seedance-2.5")
			require.Equal(t, want, ResolveTaskPlatform(c, "1", info) == constant.TaskPlatformBeeNex)
		}
	}
}
