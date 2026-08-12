package relay

import (
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relay/channel"
	taskapimartvideo "github.com/QuantumNous/new-api/relay/channel/task/apimartvideo"
	"github.com/QuantumNous/new-api/relay/channel/task/hailuo"
	relaycommon "github.com/QuantumNous/new-api/relay/common"

	"github.com/gin-gonic/gin"
)

// ResolveTaskAdaptor picks the task adaptor for the current channel + model.
func ResolveTaskAdaptor(c *gin.Context, platform constant.TaskPlatform, info *relaycommon.RelayInfo) channel.TaskAdaptor {
	modelName := strings.TrimSpace(info.OriginModelName)
	if modelName == "" {
		modelName = peekTaskModel(c)
	}
	if taskapimartvideo.IsChannel(info.ChannelBaseUrl) && taskapimartvideo.IsVideoModel(modelName) {
		return &taskapimartvideo.TaskAdaptor{}
	}
	if info.ChannelType == constant.ChannelTypeMiniMax && strings.EqualFold(modelName, "MiniMax-H3") {
		return &hailuo.H3TaskAdaptor{}
	}
	return GetTaskAdaptor(platform)
}

// ResolveTaskPlatform returns the platform string persisted on tasks.
func ResolveTaskPlatform(c *gin.Context, platform constant.TaskPlatform, info *relaycommon.RelayInfo) constant.TaskPlatform {
	modelName := strings.TrimSpace(info.OriginModelName)
	if modelName == "" {
		modelName = peekTaskModel(c)
	}
	if taskapimartvideo.IsChannel(info.ChannelBaseUrl) && taskapimartvideo.IsVideoModel(modelName) {
		return constant.TaskPlatformApimartVideo
	}
	if info.ChannelType == constant.ChannelTypeMiniMax && strings.EqualFold(modelName, "MiniMax-H3") {
		return constant.TaskPlatformMiniMaxH3
	}
	return platform
}

func peekTaskModel(c *gin.Context) string {
	if c.Request != nil {
		if m := strings.TrimSpace(c.PostForm("model")); m != "" {
			return m
		}
	}
	storage, err := common.GetBodyStorage(c)
	if err != nil {
		return ""
	}
	raw, err := storage.Bytes()
	if err != nil || len(raw) == 0 {
		return ""
	}
	var aux struct {
		Model string `json:"model"`
	}
	if err := common.Unmarshal(raw, &aux); err != nil {
		return ""
	}
	return strings.TrimSpace(aux.Model)
}
