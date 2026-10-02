package relay

import (
	"fmt"
	"net/http"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

// Quote shares normalization, routing, account pricing and input measurement
// with submission, but never reserves funds or submits work to the provider.
func QuoteSeedanceVideo(c *gin.Context, info *relaycommon.RelayInfo) (*model.SeedanceBillingDetails, *dto.TaskError) {
	if !service.IsSeedanceLibraryModel(info.OriginModelName) {
		return nil, service.TaskErrorWrapperLocal(fmt.Errorf("Quotes support the four Seedance models only"), "invalid_model", 400)
	}
	info.InitChannelMeta(c)
	platform := ResolveTaskPlatform(c, constant.TaskPlatform(c.GetString("platform")), info)
	if platform == "" {
		platform = ResolveTaskPlatform(c, GetTaskPlatform(c), info)
	}
	adaptor := ResolveTaskAdaptor(c, platform, info)
	if adaptor == nil {
		return nil, service.TaskErrorWrapperLocal(fmt.Errorf("No supported video route"), "invalid_platform", 400)
	}
	adaptor.Init(info)
	// Adapters select public-protocol normalization from the generation path.
	originalPath := c.Request.URL.Path
	c.Request.URL.Path = "/v1/videos/generations"
	taskErr := adaptor.ValidateRequestAndSetAction(c, info)
	c.Request.URL.Path = originalPath
	if taskErr != nil {
		return nil, taskErr
	}
	info.UpstreamModelName = info.OriginModelName
	if err := helper.ModelMappedHelper(c, info, nil); err != nil {
		return nil, service.TaskErrorWrapperLocal(err, "model_mapping_failed", 400)
	}
	price, err := helper.ModelPriceHelperPerCall(c, info)
	if err != nil {
		return nil, service.TaskErrorWrapperLocal(err, "price_unavailable", 400)
	}
	info.PriceData = price
	ratios, err := service.PrepareSeedanceTaskBilling(c, info)
	if err != nil {
		return nil, service.TaskErrorWrapperLocal(err, "invalid_media", http.StatusBadRequest)
	}
	info.PriceData.OtherRatios = ratios
	info.PriceData.Quota = service.SeedanceSubmissionQuota(info.PriceData)
	details, err := service.NewSeedanceBillingDetails(c, info)
	if err != nil {
		return nil, service.TaskErrorWrapperLocal(err, "quote_failed", 500)
	}
	return &details, nil
}
