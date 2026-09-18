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
	for _, name := range []string{"seedance-2.0", "doubao-seedance-2.0"} {
		info := &relaycommon.RelayInfo{
			OriginModelName: name,
			ChannelMeta:     &relaycommon.ChannelMeta{ChannelBaseUrl: "https://api.apib.ai", ChannelType: constant.ChannelTypeOpenAI},
		}
		c, _ := gin.CreateTestContext(nil)
		require.IsType(t, &apimartvideo.TaskAdaptor{}, ResolveTaskAdaptor(c, constant.TaskPlatform("sora"), info))
		require.Equal(t, constant.TaskPlatform(constant.TaskPlatformApimartVideo), ResolveTaskPlatform(c, constant.TaskPlatform("sora"), info))
	}
}
