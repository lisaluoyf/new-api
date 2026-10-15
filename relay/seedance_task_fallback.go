package relay

import (
	"fmt"
	"io"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

func init() { service.PrepareSeedanceFallbackSubmit = prepareSeedanceFallbackSubmit }

// Build/validate using the target's normal adaptor, without calling the billing
// submit pipeline. The original task already holds the customer's payment.
func prepareSeedanceFallbackSubmit(c *gin.Context, task *model.Task, ch *model.Channel, key string) (func() (*service.SeedanceFallbackResult, error), error) {
	common.SetContextKey(c, constant.ContextKeyChannelId, ch.Id)
	common.SetContextKey(c, constant.ContextKeyChannelType, ch.Type)
	common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, ch.GetBaseURL())
	common.SetContextKey(c, constant.ContextKeyChannelKey, key)
	c.Set("seedance_fallback_target_channel", ch.Id)
	info := &relaycommon.RelayInfo{UserId: task.UserId, OriginModelName: task.Properties.OriginModelName, ChannelMeta: &relaycommon.ChannelMeta{ChannelId: ch.Id, ChannelType: ch.Type, ChannelBaseUrl: ch.GetBaseURL(), ApiKey: key, ChannelSetting: ch.GetSetting(), ChannelOtherSettings: ch.GetOtherSettings(), UpstreamModelName: service.ResolveChannelUpstreamModel(ch, task.Properties.OriginModelName)}, TaskRelayInfo: &relaycommon.TaskRelayInfo{PublicTaskID: task.TaskID, Action: task.Action}}
	platform := ResolveTaskPlatform(c, constant.TaskPlatform(fmt.Sprint(ch.Type)), info)
	adaptor := ResolveTaskAdaptor(c, platform, info)
	if adaptor == nil {
		return nil, fmt.Errorf("Backup video protocol is unsupported")
	}
	adaptor.Init(info)
	if e := adaptor.ValidateRequestAndSetAction(c, info); e != nil {
		return nil, fmt.Errorf("Backup video request is unsupported")
	}
	body, err := adaptor.BuildRequestBody(c, info)
	if err != nil {
		return nil, fmt.Errorf("Backup video request could not be built")
	}
	return func() (*service.SeedanceFallbackResult, error) {
		response, err := adaptor.DoRequest(c, info, body)
		if err != nil {
			return nil, fmt.Errorf("Backup submission response is uncertain")
		}
		if response == nil {
			return nil, fmt.Errorf("Backup submission response is empty")
		}
		defer response.Body.Close()
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
			return nil, fmt.Errorf("Backup submission returned HTTP %d", response.StatusCode)
		}
		id, data, e := adaptor.DoResponse(c, response, info)
		if e != nil || id == "" {
			return nil, fmt.Errorf("Backup submission did not return a confirmed task ID")
		}
		return &service.SeedanceFallbackResult{UpstreamID: id, Platform: platform, UpstreamModel: info.UpstreamModelName, Data: data}, nil
	}, nil
}
