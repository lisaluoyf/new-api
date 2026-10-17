package service

import (
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
)

// NewSubmittedVideoTask freezes the same pricing, logical inputs and callback
// metadata before dispatch and after acceptance. It performs no billing or I/O.
func NewSubmittedVideoTask(c *gin.Context, info *relaycommon.RelayInfo, platform constant.TaskPlatform, upstreamID string, taskData []byte, quota int) *model.Task {
	task := model.InitTask(platform, info)
	task.PrivateData.Webhook = TaskWebhookConfigFromContext(c)
	task.PrivateData.UpstreamTaskID = upstreamID
	task.PrivateData.BillingSource = info.BillingSource
	task.PrivateData.SubscriptionId = info.SubscriptionId
	task.PrivateData.TokenId = info.TokenId
	task.PrivateData.BillingContext = &model.TaskBillingContext{
		SeedanceTariff:    IsSeedanceLibraryModel(info.OriginModelName),
		ModelPrice:        info.PriceData.ModelPrice,
		GroupRatio:        info.PriceData.GroupRatioInfo.GroupRatio,
		UserModelDiscount: info.PriceData.GroupRatioInfo.UserModelDiscount,
		ModelRatio:        info.PriceData.ModelRatio,
		OtherRatios:       info.PriceData.OtherRatios,
		OriginModelName:   info.OriginModelName,
		PerCallBilling:    IsTaskPerCallBilling(info.OriginModelName, info.PriceData),
	}
	if task.Platform == constant.TaskPlatformApimartVideo {
		name := strings.ToLower(info.OriginModelName)
		if IsSeedance20Variant(name) || name == "seedance-2.0" || name == "doubao-seedance-2.0" || name == "seedance-2.5" {
			if base, ok := ratio_setting.GetVideoModelBasePrice(name); ok && base > 0 && info.PriceData.ModelPrice > 0 {
				task.PrivateData.BillingContext.ProviderCostMultiplier = info.PriceData.ModelPrice / base
			}
		}
	}
	if snapshot, ok := c.Get("seedance25_normalized_request"); ok {
		task.PrivateData.SeedanceRequest = snapshot.(map[string]any)
		task.PrivateData.Key = common.GetContextKeyString(c, constant.ContextKeyChannelKey)
	}
	if snapshot, ok := c.Get("seedance20_normalized_request"); ok {
		task.PrivateData.SeedanceRequest = snapshot.(map[string]any)
		task.PrivateData.SeedanceRequest["video_input_seconds"] = c.GetInt("seedance_video_input_seconds")
	}
	if snapshot, ok := c.Get("seedance_billing_snapshot"); ok {
		if task.PrivateData.SeedanceRequest == nil {
			task.PrivateData.SeedanceRequest = map[string]any{}
		}
		for key, value := range snapshot.(map[string]any) {
			task.PrivateData.SeedanceRequest[key] = value
		}
	}
	CaptureSeedanceFallbackRequest(c, task)
	task.Quota = quota
	task.Data = taskData
	task.Action = info.Action
	if taskReq, reqErr := relaycommon.GetTaskRequest(c); reqErr == nil {
		if rd := BuildVideoRequestDataForLog(&taskReq); len(rd) > 0 {
			if encoded, merr := common.Marshal(rd); merr == nil {
				task.PrivateData.RequestData = string(encoded)
			}
		}
	}
	return task
}
