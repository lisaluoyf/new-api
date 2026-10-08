package gemini

import (
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

func recordGeminiImageOutput(c *gin.Context, info *relaycommon.RelayInfo, response *dto.GeminiChatResponse) error {
	count := 0
	for _, candidate := range response.Candidates {
		for _, part := range candidate.Content.Parts {
			if !part.Thought && part.InlineData != nil && strings.HasPrefix(part.InlineData.MimeType, "image/") && part.InlineData.Data != "" {
				count++
			}
		}
	}
	if count == 0 {
		return fmt.Errorf("upstream returned no generated image")
	}
	info.PriceData.AddOtherRatio("n", float64(count))
	if data := service.ImageRequestDataFromContext(c); data != nil {
		data["actual_image_count"] = count
	}
	return nil
}
