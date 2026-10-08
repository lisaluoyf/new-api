package helper

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
)

func nanoBanana21NativeImagePricing(c *gin.Context, info *relaycommon.RelayInfo, meta *types.TokenCountMeta) error {
	req, ok := info.Request.(*dto.GeminiChatRequest)
	if !ok || req.IsStream(c) || info.IsStream {
		return fmt.Errorf("Nano Banana 2.1 requires a non-streaming image request")
	}
	if len(req.Requests) > 0 || req.GenerationConfig.CandidateCount != nil && *req.GenerationConfig.CandidateCount != 1 {
		return fmt.Errorf("Nano Banana 2.1 requires one candidate")
	}
	if len(req.GenerationConfig.ResponseModalities) > 0 {
		hasImage := false
		for _, modality := range req.GenerationConfig.ResponseModalities {
			hasImage = hasImage || strings.EqualFold(modality, "IMAGE")
		}
		if !hasImage {
			return fmt.Errorf("Nano Banana 2.1 requires IMAGE response modality")
		}
	}
	var config struct {
		ImageSize      string `json:"imageSize"`
		ImageSizeSnake string `json:"image_size"`
		Aspect         string `json:"aspectRatio"`
		AspectSnake    string `json:"aspect_ratio"`
	}
	if len(req.GenerationConfig.ImageConfig) > 0 && common.Unmarshal(req.GenerationConfig.ImageConfig, &config) != nil {
		return fmt.Errorf("invalid imageConfig")
	}
	if config.ImageSize == "" {
		config.ImageSize = config.ImageSizeSnake
	}
	if config.Aspect == "" {
		config.Aspect = config.AspectSnake
	}
	tier := strings.ToUpper(strings.TrimSpace(config.ImageSize))
	if tier == "" {
		tier = "1K"
	}
	if tier != "1K" && tier != "2K" && tier != "4K" {
		return fmt.Errorf("unsupported imageSize")
	}
	// Canonicalize the wire value too; pricing a lowercase alias as 2K while
	// an upstream defaults an unrecognized value to 1K would overcharge.
	fields := map[string]json.RawMessage{}
	if len(req.GenerationConfig.ImageConfig) > 0 {
		if err := common.Unmarshal(req.GenerationConfig.ImageConfig, &fields); err != nil {
			return err
		}
		if fields == nil {
			fields = map[string]json.RawMessage{}
		}
	}
	fields["imageSize"], _ = common.Marshal(tier)
	delete(fields, "image_size")
	if config.Aspect != "" {
		fields["aspectRatio"], _ = common.Marshal(config.Aspect)
		delete(fields, "aspect_ratio")
	}
	req.GenerationConfig.ImageConfig, _ = common.Marshal(fields)
	meta.ImagePriceVariant = tier
	meta.ImagePriceRatio = 1
	service.SetImageRequestDataOnContext(c, &dto.ImageRequest{Model: info.OriginModelName, Resolution: tier, Size: config.Aspect, Prompt: req.GetTokenCountMeta().CombineText})
	return nil
}
